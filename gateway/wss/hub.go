package wss

import (
	"container/list"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"sync/atomic"

	"github.com/ivpusic/grpool"
	"github.com/klintcheng/fim/lgcluster"
	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/wire"

	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

const (
	useForConnect = uint8(iota + 1)
	useForDisconnect
	useForMsgRelay // 用于逻辑服务器转发过来的消息转发给客户端
)

const (
	// hub state
	closed = iota
	started
	closing
)

// ErrHubClosed ErrHubClosed
var ErrHubClosed = errors.New("err: hub is closed")

// ErrHubStarted ErrHubStarted
var ErrHubStarted = errors.New("err: hub is Started")

//Packet 对数据包的封装
type Packet struct {
	use  uint8
	body interface{}
}

// Hconfig hub config
type HubConf struct {
	JwtAppkey      string
	JwtSecret      string
	Listen         string
	SSL            bool
	MaxConnections int
}

// Gateway 是一个服务中心
// 1.监听websocket连接
// 2.维护连接
// 3.消息下发
type Gateway struct {
	ID        ksuid.KSUID
	conf      HubConf
	state     int32
	lgProxy   lgcluster.Proxy
	clients   map[ksuid.KSUID]*peer.WsClient // Peer.ID => Peer
	clientNum int32
	relayIn   chan *wire.RelayPacket
	// receive connect or disconnect or message dequeue from logic server
	enqueue chan *Packet
	dequeue chan *Packet
	next    chan struct{}
	quit    *peer.Event
	log     *log.Entry
}

// NewGateway 创建一个 Server 对象，并初始化
func NewGateway(id ksuid.KSUID, conf HubConf, lgProxy lgcluster.Proxy) (*Gateway, error) {
	// receive message from logic server
	relayIn := make(chan *wire.RelayPacket, 1)

	hub := &Gateway{
		conf:    conf,
		state:   closed,
		relayIn: relayIn,
		lgProxy: lgProxy,
		enqueue: make(chan *Packet, 1),
		dequeue: make(chan *Packet, 1),
		next:    make(chan struct{}, 1),
		clients: make(map[ksuid.KSUID]*peer.WsClient, 1000),
		quit:    peer.NewEvent(),
		log:     log.WithField("sid", id.String()),
	}

	return hub, nil
}

func (g *Gateway) Receive(pkt *wire.RelayPacket) error {
	g.relayIn <- pkt
	return nil
}

// 处理消息queue
func (g *Gateway) packetPushEnqueueHandler() {
	g.log.Info("packetPushEnqueueHandler start")
	pendingMsgs := list.New()

	// We keep the waiting flag so that we know if we have a pending message
	waiting := false

	// To avoid duplication below.
	queuePacket := func(packet *Packet, list *list.List, waiting bool) bool {
		if !waiting {
			g.dequeue <- packet
		} else {
			list.PushBack(packet)
		}
		// we are always waiting now.
		return true
	}
	for {
		select {
		case packet := <-g.enqueue:
			waiting = queuePacket(packet, pendingMsgs, waiting)
		case <-g.next:
			next := pendingMsgs.Front()
			if next == nil {
				waiting = false
				continue
			}
			val := pendingMsgs.Remove(next)
			g.dequeue <- val.(*Packet)
		}
	}
}

// its safe to operate map clients ,due to we do get ,remove ,add client enqueue one goroutine
func (g *Gateway) packetPushDequeueHandler() {
	g.log.Info("packetPushDequeueHandler start")
	for packet := range g.dequeue {
		if packet == nil || packet.body == nil {
			g.log.Warn("bad packet", packet)
			continue
		}
		switch packet.use {
		case useForConnect:
			g.handleClientConnect(packet.body.(*peer.WsClient))
		case useForDisconnect:
			g.handleClientDisconnect(packet.body.(*peer.WsClient))
		case useForMsgRelay:
			g.handleMsgRelay(packet.body.(*wire.RelayPacket))
		}
		g.next <- struct{}{}
	}
}

func (g *Gateway) handleClientConnect(client *peer.WsClient) {
	if _, has := g.clients[client.ID]; has {
		g.log.Warn("client id repeated", client.ID)
	}
	g.clients[client.ID] = client
	//	send a login package to login server
	message := wire.NewMessage(wire.PIDLogin, peer.Seq.Next())
	_ = message.Write(&wire.LoginReq{
		GatewayId: g.ID.String(),
		PeerId:    client.ID.String(),
		Username:  client.Attrs.Username,
		Device:    wire.DeviceType(client.Attrs.Device),
		RemoteIp:  client.Attrs.RemoteIP,
	})
	relayMessage := wire.NewEmptyRelayMessage(g.ID)

	_ = g.lgProxy.Send(relayMessage)

	atomic.AddInt32(&g.clientNum, 1)
	clientTotal.WithLabelValues(g.ID.String()).Set(float64(len(g.clients)))
}

func (g *Gateway) handleClientDisconnect(client *peer.WsClient) {
	if _, has := g.clients[client.ID]; !has {
		return
	}
	delete(g.clients, client.ID)

	//	send a logout package to login server
	g.sendDisconnectPacket(client.ID)

	atomic.AddInt32(&g.clientNum, -1)
	clientTotal.WithLabelValues(g.ID.String()).Set(float64(len(g.clients)))
}

func (g *Gateway) sendDisconnectPacket(peerID ksuid.KSUID) {
	message := wire.NewMessage(wire.PIDLogout, peer.Seq.Next())
	_ = message.Write(&wire.LogoutReq{
		PeerId: peerID.String(),
	})
	relayMessage := wire.NewEmptyRelayMessage(g.ID)

	_ = g.lgProxy.Send(relayMessage)
}

// transfer payload to receivers
func (g *Gateway) handleMsgRelay(message *wire.RelayPacket) {
	if len(message.Receivers) == 0 {
		return
	}
	aff := 0
	header := message.Payload.Header

	for _, recv := range message.Receivers {
		if c, has := g.clients[recv]; has {
			err := c.Push(message.Payload.Bytes())
			if err != nil {
				g.log.Error(err)
				pushErrorTotal.WithLabelValues(g.ID.String(), header.GetPID().String()).Inc()
			}
			aff++
		} else {
			g.log.Debugf("client no found, peer_id:%s", recv.String())
			g.sendDisconnectPacket(recv)
		}
	}
	if aff == 0 {
		return
	}

	log.WithFields(log.Fields{
		"affected": aff,
		"to":       message.Receivers,
	}).Debug("message delivery: ", header)

	messageTotal.WithLabelValues(g.ID.String(), header.GetPID().String(), msgDirectionPush).Inc()

	// handle kickOut message
	if header.GetPID() == wire.PIDKickOut {
		kickOut, _ := message.Payload.GetPayloadEntity()
		pid, _ := ksuid.Parse(kickOut.(*wire.KickOut).PeerId)
		if client, has := g.clients[pid]; has {
			g.log.Debug("kickout peerID: ", client.ID)
			delete(g.clients, pid)
			client.Close()
		}
	}
}

// Listen start Listen
func (g *Gateway) listen(lst *peer.ClientListener) error {
	ssl, addr := g.conf.SSL, g.conf.Listen

	g.log.Info("tcp server started, Listen ", addr)
	var ln net.Listener
	var err error
	if ssl {
		cert, cerr := tls.LoadX509KeyPair("certs/server.pem", "certs/server.key")
		if cerr != nil {
			log.Fatalf("server: loadkeys: %s", err)
			return cerr
		}
		config := tls.Config{Certificates: []tls.Certificate{cert}}
		config.Rand = rand.Reader
		ln, err = tls.Listen("tcp", addr, &config)
	} else {
		ln, err = net.Listen("tcp", addr)
	}

	if err != nil {
		return err
	}
	defer ln.Close()
	pool := grpool.NewPool(200, 1000)
	for {
		if !g.IsOpen() {
			return nil
		}
		conn, err := ln.Accept()
		if err != nil {
			g.log.Error(err)
			continue
		}

		pool.JobQueue <- func() {
			if err = upgradeConn(g, conn, lst); err != nil {
				conn.Close()
				g.log.Debug(err)
			}
		}
	}

}

// Start start all handlers
func (g *Gateway) Start() error {
	if !atomic.CompareAndSwapInt32(&g.state, closed, started) {
		return ErrHubStarted
	}

	// handle message from client or client disconnect
	lst := &peer.ClientListener{
		OnMsg:        make(chan *peer.ClientMessage, 100),
		OnDisconnect: make(chan *peer.WsClient, 1),
	}

	go g.packetPushEnqueueHandler()
	go g.packetPushDequeueHandler()

	go g.handleClientEvents(lst)
	go g.handleSeverEvents()

	// start http server latest
	go func() {
		if err := g.listen(lst); err != nil {
			log.Panic(err)
		}
	}()

	log.Infof("gateway[%v] start up", g.ID.String())
	return nil
}

// handle client peer message
// 1. decode a message from client , send to hub.enqueue
// 2. wrap a disconnected connection ,send to lgProxy.enqueue
func (g *Gateway) handleClientEvents(lst *peer.ClientListener) {
	for {
		select {
		case recvMsg := <-lst.OnMsg:
			pid := recvMsg.Payload.Header.GetPID()
			if !wire.IsProtocol(pid) {
				log.Warn("invaild PID:", pid)
				continue
			}
			log.Trace(recvMsg.Payload)
			messageFlowBytes.WithLabelValues(g.ID.String(), pid.String()).Add(float64(len(recvMsg.Payload.Payload)))
			messageTotal.WithLabelValues(g.ID.String(), pid.String(), msgDirectionUp).Inc()

			relayMsg := wire.NewRelayMessage(recvMsg.ID, recvMsg.Attrs.Username, recvMsg.Attrs.Device, g.ID)
			relayMsg.Payload = recvMsg.Payload

			_ = g.lgProxy.Send(relayMsg)

		case client := <-lst.OnDisconnect:
			_ = g.Push(&Packet{useForDisconnect, client})
		}
	}
}

// build a packet with dequeue message from logic server，and send to channel enqueue of hub
func (g *Gateway) handleSeverEvents() {
	for msg := range g.relayIn {
		g.enqueue <- &Packet{useForMsgRelay, msg}
	}
}

// Push push packet to hub
func (g *Gateway) Push(p *Packet) error {
	if atomic.LoadInt32(&g.state) != started {
		return ErrHubClosed
	}
	g.enqueue <- p
	return nil
}

// Shutdown graceful shut down hub
func (g *Gateway) Shutdown() error {
	if !atomic.CompareAndSwapInt32(&g.state, started, closing) {
		return ErrHubClosed
	}
	defer atomic.CompareAndSwapInt32(&g.state, closing, closed)

	for _, client := range g.clients {
		g.sendDisconnectPacket(client.ID)
		client.Close()
		log.Infof("close client %s %s %s", client.Attrs.Username, client.Attrs.Username, client.Attrs.RemoteIP)
	}

	g.clients = nil
	g.lgProxy = nil

	return nil
}

//IsOpen detect whether is hub open
func (g *Gateway) IsOpen() bool {
	return atomic.LoadInt32(&g.state) == started
}

type Token struct {
	Username string
	Device   string
}

// TODO: pasre tokenn
func (g *Gateway) parseToken(tk string) (*Token, error) {

	return nil, nil
}
