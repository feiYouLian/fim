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

	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/server/handler"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
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

// // ProtocolHandle ProtocolHandle
// type ProtocolHandle func(msg *wire.RelayPacket, out chan<- *wire.LocMessage)

type HubConf struct {
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

// Hub 是一个服务中心
// 1.监听websocket连接
// 2.维护连接
// 3.消息下发
type Hub struct {
	ID ksuid.KSUID //lgserver id
	sync.Mutex
	state        int32 // 0 closed 1 started 2 closing
	conf         HubConf
	gateways     map[ksuid.KSUID]*peer.Server
	lgservers    map[ksuid.KSUID]*peer.Server
	out          chan *LocMessage
	connect      chan *peer.Server
	protoHandler *handler.MessageHandler
	log          *log.Entry
}

// NewHub 创建一个 Server 对象，并初始化
func NewHub(id ksuid.KSUID, conf HubConf) (*Hub, error) {
	hub := &Hub{
		conf:     conf,
		state:    closed,
		connect:  make(chan *peer.Server, 1),
		out:      make(chan *LocMessage, 100),
		gateways: make(map[ksuid.KSUID]*peer.Server, 5),
		log:      log.WithField("ID", id).WithField("Loc", "hub"),
	}

	protoHandler, err := handler.NewMessageHandler(id, hub)
	if err != nil {
		return nil, err
	}

	hub.protoHandler = protoHandler
	return hub, nil
}

// Start start all handlers
func (h *Hub) Start() error {
	if !atomic.CompareAndSwapInt32(&h.state, closed, started) {
		return ErrHubStarted
	}

	// handle message from client or client disconnect
	lst := &peer.ServerListener{
		OnMsg:        make(chan *peer.ServerMessage, 100),
		OnDisconnect: make(chan *peer.Server, 1),
	}

	go h.protoHandler.Start()

	go h.readHandler(lst.OnMsg)
	go h.writeHandler()
	go h.gatewayHandler(lst.OnDisconnect)

	// start http server latest
	go func() {
		_ = h.listen(lst)
	}()

	h.log.Infof("start up")
	return nil
}

// readhandler read message
func (h *Hub) readHandler(onMsg chan *peer.ServerMessage) {
	for msg := range onMsg { // receive message from gateway
		buf := bytes.NewBuffer(msg.Payload)
		relayMsg := new(wire.RelayPacket)
		if err := relayMsg.Decode(buf); err != nil {
			if err != io.EOF {
				h.log.Error(err)
			}
			break
		}

		//	handle message
		h.protoHandler.Send(relayMsg)
	}
}

// writeHandler wirte message to gateway
func (h *Hub) writeHandler() {
	for locMessage := range h.out { // push message to gateway
		h.Lock()
		gateway, has := h.gateways[locMessage.ServerID]
		h.Unlock()

		if !has {
			h.log.Warn("Gateway no found: ID:", locMessage.ServerID)
			continue
		}
		buf := &bytes.Buffer{}
		err := locMessage.Payload.Encode(buf)
		if err != nil {
			h.log.Error("locMessage encode error", err.Error())
			continue
		}
		if err := gateway.Push(buf.Bytes()); err != nil {
			h.log.Error("send locMessage failed", err.Error())
			writeGwFailTotal.WithLabelValues(h.ID.String(), gateway.ID.String()).Inc()
		}
	}
}

// handle gateway connect or disconnect
func (h *Hub) gatewayHandler(onDisconnect chan *peer.Server) {
	for {
		select {
		case p := <-h.connect:
			h.Lock()
			h.gateways[p.ID] = p
			h.Unlock()
			h.log.Infof("gateway [%s] connected", p.ID.String())
		case p := <-onDisconnect:
			h.Lock()
			delete(h.gateways, p.ID)
			h.Unlock()
			h.log.Infof("gateway [%s] disconnected", p.ID.String())
		}
	}
}

// Shutdown graceful shut down hub
func (h *Hub) Shutdown(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&h.state, started, closing) {
		return ErrHubClosed
	}
	defer atomic.CompareAndSwapInt32(&h.state, closing, closed)

	h.Lock()
	defer h.Unlock()
	for _, s := range h.gateways {
		s.Close()
	}
	h.gateways = nil
	return nil
}

// Listen start Listen
func (h *Hub) listen(lst *peer.ServerListener) error {
	h.log.Info("tcp server started, Listen ", h.conf.ListenTCP)

	ln, err := net.Listen("tcp", h.conf.ListenTCP)
	if err != nil {
		h.log.Error(err)
		return err
	}
	defer ln.Close()

	handshake := peer.Handshake{
		OnHandshake: func(req peer.HandshakeReq) (uint16, error) {
			h.Lock()
			defer h.Unlock()
			if _, has := h.gateways[req.SID]; has {
				return peer.CodeHandshakeDuplicatedSID, fmt.Errorf("server existed")
			}
			if _, has := h.lgservers[req.SID]; has {
				return peer.CodeHandshakeDuplicatedSID, fmt.Errorf("server existed")
			}
			return peer.CodeHandshakeOk, nil
		},
	}

	for {
		if atomic.LoadInt32(&h.state) != started {
			return nil
		}
		conn, err := ln.Accept()
		if err != nil {
			h.log.Error(err)
			continue
		}
		req, err := handshake.Upgrade(conn)
		if err != nil {
			h.log.Infof("upgrade error: %s", err)
			conn.Close()
			continue
		}

		server := peer.NewServer(req.SID, req.Meta, lst)
		server.Addr = conn.RemoteAddr().String()
		err = server.Start(conn, peer.SideServer)
		if err != nil {
			h.log.Error(err)
			continue
		}

		h.connect <- server
	}

}

func (h *Hub) To(gateway ksuid.KSUID, pkg *wire.RelayPacket) error {
	// TODO: handle
	return nil
}
