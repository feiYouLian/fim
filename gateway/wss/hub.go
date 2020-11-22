package wshub

import (
	"container/list"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"sync/atomic"

	"github.com/ivpusic/grpool"
	"github.com/klintcheng/fim/gateway/lgserver"
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

// Hub 是一个服务中心
// 1.监听websocket连接
// 2.维护连接
// 3.消息下发
type Hub struct {
	ID        ksuid.KSUID
	conf      HubConf
	state     int32
	lgm       *lgserver.Manager
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

// NewHub 创建一个 Server 对象，并初始化
func NewHub(id ksuid.KSUID, conf HubConf, namesrv lgserver.Namesrv) (*Hub, error) {
	var err error
	// receive message from logic server
	relayIn := make(chan *wire.RelayPacket, 1)

	lgm, err := lgserver.NewManager(id, namesrv, relayIn)
	if err != nil {
		return nil, err
	}

	hub := &Hub{
		conf:    conf,
		lgm:     lgm,
		state:   closed,
		relayIn: relayIn,
		enqueue: make(chan *Packet, 1),
		dequeue: make(chan *Packet, 1),
		next:    make(chan struct{}, 1),
		clients: make(map[ksuid.KSUID]*peer.WsClient, 1000),
		quit:    peer.NewEvent(),
		log:     log.WithField("sid", id.String()),
	}

	return hub, nil
}

// 处理消息queue
func (h *Hub) packetPushEnqueueHandler() {
	h.log.Info("packetPushEnqueueHandler start")
	pendingMsgs := list.New()

	// We keep the waiting flag so that we know if we have a pending message
	waiting := false

	// To avoid duplication below.
	queuePacket := func(packet *Packet, list *list.List, waiting bool) bool {
		if !waiting {
			h.dequeue <- packet
		} else {
			list.PushBack(packet)
		}
		// we are always waiting now.
		return true
	}
	for {
		select {
		case packet := <-h.enqueue:
			waiting = queuePacket(packet, pendingMsgs, waiting)
		case <-h.next:
			next := pendingMsgs.Front()
			if next == nil {
				waiting = false
				continue
			}
			val := pendingMsgs.Remove(next)
			h.dequeue <- val.(*Packet)
		}
	}
}

// its safe to operate map clients ,due to we do get ,remove ,add client enqueue one goroutine
func (h *Hub) packetPushDequeueHandler() {
	h.log.Info("packetPushDequeueHandler start")
	for packet := range h.dequeue {
		if packet == nil || packet.body == nil {
			h.log.Warn("bad packet", packet)
			continue
		}
		switch packet.use {
		case useForConnect:
			h.handleClientConnect(packet.body.(*peer.WsClient))
		case useForDisconnect:
			h.handleClientDisconnect(packet.body.(*peer.WsClient))
		case useForMsgRelay:
			h.handleMsgRelay(packet.body.(*wire.RelayPacket))
		}
		h.next <- struct{}{}
	}
}

func (h *Hub) handleClientConnect(client *peer.WsClient) {
	if _, has := h.clients[client.ID]; has {
		h.log.Warn("client id repeated", client.ID)
	}
	h.clients[client.ID] = client
	//	send a login package to login server
	message := wire.NewMessage(wire.PIDLogin, peer.Seq.Next())
	_ = message.Write(&wire.LoginReq{
		GatewayId: h.ID.String(),
		PeerId:    client.ID.String(),
		Username:  client.Attrs.Username,
		Device:    wire.DeviceType(client.Attrs.Device),
		RemoteIp:  client.Attrs.RemoteIP,
	})
	relayMessage := wire.NewEmptyRelayMessage(h.ID)
	h.lgm.In() <- relayMessage
	atomic.AddInt32(&h.clientNum, 1)
	clientTotal.WithLabelValues(h.ID.String()).Set(float64(len(h.clients)))
}

func (h *Hub) handleClientDisconnect(client *peer.WsClient) {
	if _, has := h.clients[client.ID]; !has {
		return
	}
	delete(h.clients, client.ID)

	//	send a logout package to login server
	h.sendDisconnectPacket(client.ID)

	atomic.AddInt32(&h.clientNum, -1)
	clientTotal.WithLabelValues(h.ID.String()).Set(float64(len(h.clients)))
}

func (h *Hub) sendDisconnectPacket(peerID ksuid.KSUID) {
	message := wire.NewMessage(wire.PIDLogout, peer.Seq.Next())
	_ = message.Write(&wire.LogoutReq{
		PeerId: peerID.String(),
	})
	relayMessage := wire.NewEmptyRelayMessage(h.ID)
	h.lgm.In() <- relayMessage
}

// transfer payload to receivers
func (h *Hub) handleMsgRelay(message *wire.RelayPacket) {
	if len(message.Receivers) == 0 {
		return
	}
	aff := 0
	header := message.Payload.Header

	for _, recv := range message.Receivers {
		if c, has := h.clients[recv]; has {
			err := c.Push(message.Payload.Bytes())
			if err != nil {
				h.log.Error(err)
				pushErrorTotal.WithLabelValues(h.ID.String(), header.GetPID().String()).Inc()
			}
			aff++
		} else {
			h.log.Debugf("client no found, peer_id:%s", recv.String())
			h.sendDisconnectPacket(recv)
		}
	}
	if aff == 0 {
		return
	}

	log.WithFields(log.Fields{
		"affected": aff,
		"to":       message.Receivers,
	}).Debug("message delivery: ", header)

	messageTotal.WithLabelValues(h.ID.String(), header.GetPID().String(), msgDirectionPush).Inc()

	// handle kickOut message
	if header.GetPID() == wire.PIDKickOut {
		kickOut, _ := message.Payload.GetPayloadEntity()
		pid, _ := ksuid.Parse(kickOut.(*wire.KickOut).PeerId)
		if client, has := h.clients[pid]; has {
			h.log.Debug("kickout peerID: ", client.ID)
			delete(h.clients, pid)
			client.Close()
		}
	}
}

// Listen start Listen
func (h *Hub) listen(lst *peer.ClientListener) error {
	ssl, addr := h.conf.SSL, h.conf.Listen

	h.log.Info("tcp server started, Listen ", addr)
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
		if !h.IsOpen() {
			return nil
		}
		conn, err := ln.Accept()
		if err != nil {
			h.log.Error(err)
			continue
		}

		pool.JobQueue <- func() {
			if err = upgradeConn(h, conn, lst); err != nil {
				conn.Close()
				h.log.Debug(err)
			}
		}
	}

}

// Start start all handlers
func (h *Hub) Start() error {
	if !atomic.CompareAndSwapInt32(&h.state, closed, started) {
		return ErrHubStarted
	}

	// handle message from client or client disconnect
	lst := &peer.ClientListener{
		OnMsg:        make(chan *peer.ClientMessage, 100),
		OnDisconnect: make(chan *peer.WsClient, 1),
	}

	h.lgm.Start(true)

	go h.packetPushEnqueueHandler()
	go h.packetPushDequeueHandler()

	go h.handleClientEvents(lst)
	go h.handleSeverEvents()

	// start http server latest
	go func() {
		if err := h.listen(lst); err != nil {
			log.Panic(err)
		}
	}()

	log.Infof("gateway[%v] start up", h.ID.String())
	return nil
}

// handle client peer message
// 1. decode a message from client , send to hub.enqueue
// 2. wrap a disconnected connection ,send to lgm.enqueue
func (h *Hub) handleClientEvents(lst *peer.ClientListener) {
	for {
		select {
		case recvMsg := <-lst.OnMsg:
			pid := recvMsg.Payload.Header.GetPID()
			if !wire.IsProtocol(pid) {
				log.Warn("invaild PID:", pid)
				continue
			}
			log.Trace(recvMsg.Payload)
			messageFlowBytes.WithLabelValues(h.ID.String(), pid.String()).Add(float64(len(recvMsg.Payload.Payload)))
			messageTotal.WithLabelValues(h.ID.String(), pid.String(), msgDirectionUp).Inc()

			relayMsg := wire.NewRelayMessage(recvMsg.ID, recvMsg.Attrs.Username, recvMsg.Attrs.Device, h.ID)
			relayMsg.Payload = recvMsg.Payload

			h.lgm.In() <- relayMsg
		case client := <-lst.OnDisconnect:
			_ = h.Push(&Packet{useForDisconnect, client})
		}
	}
}

// build a packet with dequeue message from logic server，and send to channel enqueue of hub
func (h *Hub) handleSeverEvents() {
	for msg := range h.relayIn {
		h.enqueue <- &Packet{useForMsgRelay, msg}
	}
}

// Push push packet to hub
func (h *Hub) Push(p *Packet) error {
	if atomic.LoadInt32(&h.state) != started {
		return ErrHubClosed
	}
	h.enqueue <- p
	return nil
}

// Shutdown graceful shut down hub
func (h *Hub) Shutdown() error {
	if !atomic.CompareAndSwapInt32(&h.state, started, closing) {
		return ErrHubClosed
	}
	defer atomic.CompareAndSwapInt32(&h.state, closing, closed)

	for _, client := range h.clients {
		h.sendDisconnectPacket(client.ID)
		client.Close()
		log.Infof("close client %s %s %s", client.Attrs.Username, client.Attrs.Username, client.Attrs.RemoteIP)
	}

	h.lgm.Shutdown()

	h.clients = nil
	h.lgm = nil

	return nil
}

//IsOpen detect whether is hub open
func (h *Hub) IsOpen() bool {
	return atomic.LoadInt32(&h.state) == started
}

type Token struct {
	Username string
	Device   string
}

// TODO: pasre tokenn
func (h *Hub) parseToken(tk string) (*Token, error) {

	return nil, nil
}
