package lgserver

import (
	"bytes"

	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

// Manager 代表逻辑服务中心，它有两个作用
// 1. 维护逻辑服务器节点
// 2. 服务路由
// 3. 消息上行转发
// 4. 处理来自逻辑服务器的数据包，解析并转发给hub处理
type Manager struct {
	ID ksuid.KSUID
	// store server list
	bu   *cluster
	in   chan *wire.RelayPacket   // receive from client
	msg  chan *peer.ServerMessage //receive from logic server
	out  chan<- *wire.RelayPacket
	conf *Config
	log  *log.Entry
}

// Config Config
type Config struct {
	GatewayID ksuid.KSUID
}

// Namesrv discovery server
type Namesrv interface {
	GetServers() ([]wire.LgServer, error)
}

// NewManager NewManager
func NewManager(id ksuid.KSUID, namesrv Namesrv, out chan<- *wire.RelayPacket) (*Manager, error) {

	push := make(chan *peer.ServerMessage, 100)
	serverHub := &Manager{
		ID:  id,
		bu:  newCluster(id, namesrv, push),
		msg: push,
		in:  make(chan *wire.RelayPacket, 100),
		out: out,
		log: log.WithField("ID", id).WithField("Loc", "lg_manager"),
	}
	return serverHub, nil
}

//Start Start Manager
func (m *Manager) Start(block bool) {
	//	init all logicServer
	m.bu.start()

	// handle uploading message
	go m.handleClientMessage()
	// transfer pushing message
	go m.handleLogicServerMsg()
}

//Shutdown close all server peer
func (m *Manager) Shutdown() {
	m.bu.exit()
	m.log.Info("Manager shutdown")
}

// 1.server route
// 2.message dequeue
func (m *Manager) handleClientMessage() {
	for msg := range m.in {
		m.log.Infof("push to server %v", msg.Payload.Header)
		server := m.bu.Get(msg.Payload.Header.GetPID())
		if server == nil {
			m.log.Warn("throw message")
			writeLogicFailTotal.WithLabelValues(m.ID.String()).Inc()
			continue
		}
		err := server.Push(msg.Bytes())
		if err != nil {
			m.log.Error(err)
			continue
		}
	}
}

func (m *Manager) handleLogicServerMsg() {
	for msg := range m.msg { // message from logic server
		relay := new(wire.RelayPacket)
		err := relay.Decode(bytes.NewBuffer(msg.Payload))
		if err != nil {
			m.log.Error(err)
			continue
		}
		// send to hub
		m.out <- relay
	}
}

//In message in
func (m *Manager) In() chan *wire.RelayPacket {
	return m.in
}
