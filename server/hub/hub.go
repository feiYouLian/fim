package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/klintcheng/fim/lgcluster"
	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/server/handler"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

const (
	// lg state
	closed = iota
	started
	closing
)

// ErrHubClosed ErrHubClosed
var ErrHubClosed = errors.New("err: lg is closed")

// ErrHubStarted ErrHubStarted
var ErrHubStarted = errors.New("err: lg is Started")

// // ProtocolHandle ProtocolHandle
// type ProtocolHandle func(msg *wire.RelayPacket, out chan<- *wire.LocMessage)

type ServerConfig struct {
	ListenTCP string
}

type Store interface {
	Put(key string, Val []byte) error
	Get(key string) ([]byte, error)
}

type LocMessage struct {
	ServerID ksuid.KSUID
	Payload  *wire.RelayPacket
}

// LgServer 是一个服务中心
// 1.监听websocket连接
// 2.维护连接
// 3.消息下发
type LgServer struct {
	sync.Mutex
	ID           ksuid.KSUID //lgserver id
	lgProxy      lgcluster.Proxy
	state        int32 // 0 closed 1 started 2 closing
	conf         ServerConfig
	gateways     map[ksuid.KSUID]*peer.Server
	lgservers    map[ksuid.KSUID]*peer.Server
	out          chan *LocMessage
	connect      chan *peer.Server
	protoHandler *handler.MessageHandler
	log          *log.Entry
}

// NewLgServer 创建一个 Server 对象，并初始化
func NewLgServer(id ksuid.KSUID, conf ServerConfig, proxy lgcluster.Proxy) (*LgServer, error) {
	lg := &LgServer{
		conf:     conf,
		state:    closed,
		lgProxy:  proxy,
		connect:  make(chan *peer.Server, 1),
		out:      make(chan *LocMessage, 100),
		gateways: make(map[ksuid.KSUID]*peer.Server, 5),
		log:      log.WithField("ID", id).WithField("Loc", "logicserver"),
	}

	protoHandler, err := handler.NewMessageHandler(id, lg)
	if err != nil {
		return nil, err
	}

	lg.protoHandler = protoHandler
	return lg, nil
}

// Start start all handlers
func (lg *LgServer) Start() error {
	if !atomic.CompareAndSwapInt32(&lg.state, closed, started) {
		return ErrHubStarted
	}

	// handle message from client or client disconnect
	lst := &peer.ServerListener{
		OnMsg:        make(chan *peer.ServerMessage, 100),
		OnDisconnect: make(chan *peer.Server, 1),
	}

	go lg.protoHandler.Start()

	go lg.readHandler(lst.OnMsg)
	go lg.writeHandler()
	go lg.gatewayHandler(lst.OnDisconnect)

	// start http server latest
	go func() {
		_ = lg.listen(lst)
	}()

	lg.log.Infof("start up")
	return nil
}

// readhandler read message
func (lg *LgServer) readHandler(onMsg chan *peer.ServerMessage) {
	for msg := range onMsg { // receive message from gateway
		buf := bytes.NewBuffer(msg.Payload)
		relayMsg := new(wire.RelayPacket)
		if err := relayMsg.Decode(buf); err != nil {
			if err != io.EOF {
				lg.log.Error(err)
			}
			break
		}

		//	handle message
		lg.protoHandler.Send(relayMsg)
	}
}

// writeHandler wirte message to gateway
func (lg *LgServer) writeHandler() {
	for locMessage := range lg.out { // push message to gateway
		lg.Lock()
		gateway, has := lg.gateways[locMessage.ServerID]
		lg.Unlock()

		if !has {
			lg.log.Warn("Gateway no found: ID:", locMessage.ServerID)
			continue
		}
		buf := &bytes.Buffer{}
		err := locMessage.Payload.Encode(buf)
		if err != nil {
			lg.log.Error("locMessage encode error", err.Error())
			continue
		}
		if err := gateway.Push(buf.Bytes()); err != nil {
			lg.log.Error("send locMessage failed", err.Error())
			writeGwFailTotal.WithLabelValues(lg.ID.String(), gateway.ID.String()).Inc()
		}
	}
}

// handle gateway connect or disconnect
func (lg *LgServer) gatewayHandler(onDisconnect chan *peer.Server) {
	for {
		select {
		case p := <-lg.connect:
			lg.Lock()
			lg.gateways[p.ID] = p
			lg.Unlock()
			lg.log.Infof("gateway [%s] connected", p.ID.String())
		case p := <-onDisconnect:
			lg.Lock()
			delete(lg.gateways, p.ID)
			lg.Unlock()
			lg.log.Infof("gateway [%s] disconnected", p.ID.String())
		}
	}
}

// Shutdown graceful shut down lg
func (lg *LgServer) Shutdown(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&lg.state, started, closing) {
		return ErrHubClosed
	}
	defer atomic.CompareAndSwapInt32(&lg.state, closing, closed)

	lg.Lock()
	defer lg.Unlock()
	for _, s := range lg.gateways {
		s.Close()
	}
	lg.gateways = nil
	return nil
}

// Listen start Listen
func (lg *LgServer) listen(lst *peer.ServerListener) error {
	lg.log.Info("tcp server started, Listen ", lg.conf.ListenTCP)

	ln, err := net.Listen("tcp", lg.conf.ListenTCP)
	if err != nil {
		lg.log.Error(err)
		return err
	}
	defer ln.Close()

	handshake := peer.Handshake{
		OnHandshake: func(req peer.HandshakeReq) (uint16, error) {
			lg.Lock()
			defer lg.Unlock()
			if _, has := lg.gateways[req.SID]; has {
				return peer.CodeHandshakeDuplicatedSID, fmt.Errorf("server existed")
			}
			if _, has := lg.lgservers[req.SID]; has {
				return peer.CodeHandshakeDuplicatedSID, fmt.Errorf("server existed")
			}
			return peer.CodeHandshakeOk, nil
		},
	}

	for {
		if atomic.LoadInt32(&lg.state) != started {
			return nil
		}
		conn, err := ln.Accept()
		if err != nil {
			lg.log.Error(err)
			continue
		}
		req, err := handshake.Upgrade(conn)
		if err != nil {
			lg.log.Infof("upgrade error: %s", err)
			conn.Close()
			continue
		}

		server := peer.NewServer(req.SID, req.Meta, lst)
		server.Addr = conn.RemoteAddr().String()
		err = server.Start(conn, peer.SideServer)
		if err != nil {
			lg.log.Error(err)
			continue
		}

		lg.connect <- server
	}

}

func (lg *LgServer) To(gateway ksuid.KSUID, pkg *wire.RelayPacket) error {
	// TODO: handle
	return nil
}
