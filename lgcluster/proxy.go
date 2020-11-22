package lgcluster

import (
	"bytes"

	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

type Proxy interface {
	Send(pkt *wire.RelayPacket) error
}

// Proxy 代表逻辑服务中心，它有两个作用
// 1. 维护逻辑服务器节点
// 2. 服务路由
// 3. 消息上行转发
// 4. 处理来自逻辑服务器的数据包，解析并转发给hub处理
type LgClusterProxy struct {
	ID ksuid.KSUID
	// store server list
	bu      *cluster
	receive Receive
	in      chan *wire.RelayPacket   // receive from client
	msg     chan *peer.ServerMessage //receive from logic server
	log     *log.Entry
}

// Config Config
type Config struct {
	GatewayID ksuid.KSUID
}

type Receive func(pkt *wire.RelayPacket) error

// Namesrv discovery server
type Namesrv interface {
	GetServers() ([]wire.LgServer, error)
}

// NewManager NewManager
func NewLgClusterProxy(id ksuid.KSUID, namesrv Namesrv, receive func(pkt *wire.RelayPacket) error) (*LgClusterProxy, error) {
	push := make(chan *peer.ServerMessage, 100)

	proxy := &LgClusterProxy{
		ID:      id,
		bu:      newCluster(id, namesrv, push),
		msg:     push,
		in:      make(chan *wire.RelayPacket, 100),
		receive: receive,
		log:     log.WithField("ID", id).WithField("Loc", "lg_manager"),
	}

	if receive != nil {
		proxy.receive = func(pkt *wire.RelayPacket) error {
			return nil
		}
	}
	return proxy, nil
}

func (p *LgClusterProxy) SetReceiver(receive func(pkt *wire.RelayPacket) error) {
	p.receive = receive
}

//Start Start Proxy
func (p *LgClusterProxy) Start(block bool) {
	//	init all logicServer
	p.bu.start()

	// handle uploading message
	go p.handleClientMessage()
	// transfer pushing message
	go p.handleLogicServerMsg()
}

//Shutdown close all server peer
func (p *LgClusterProxy) Shutdown() {
	p.bu.exit()
	p.log.Info("Proxy shutdown")
}

// 1.server route
// 2.message dequeue
func (p *LgClusterProxy) handleClientMessage() {
	for msg := range p.in {
		p.log.Infof("push to server %v", msg.Payload.Header)
		server := p.bu.Get(msg.Payload.Header.GetPID())
		if server == nil {
			p.log.Warn("throw message")
			writeLogicFailTotal.WithLabelValues(p.ID.String()).Inc()
			continue
		}
		err := server.Push(msg.Bytes())
		if err != nil {
			p.log.Error(err)
			continue
		}
	}
}

func (p *LgClusterProxy) handleLogicServerMsg() {
	for msg := range p.msg { // message from logic server
		pkt := new(wire.RelayPacket)
		err := pkt.Decode(bytes.NewBuffer(msg.Payload))
		if err != nil {
			p.log.Error(err)
			continue
		}
		// send to hub
		_ = p.receive(pkt)
	}
}

//In message in
func (p *LgClusterProxy) Send(pkt *wire.RelayPacket) error {
	p.in <- pkt
	return nil
}
