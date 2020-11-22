package lgcluster

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

const (
	TypeGate = "gateway"
)

//cluster mange all server .include :
// 1. load all logic server
// 2. handle server's disconnect
// 3. handle server's register and discovery
type cluster struct {
	sync.RWMutex
	gwid    ksuid.KSUID // gateway id
	servers map[ksuid.KSUID]*peer.Server
	ids     []ksuid.KSUID
	index   int
	namesrv Namesrv
	lst     *peer.ServerListener
	log     *log.Entry
	started int32
}

func newCluster(id ksuid.KSUID, namesrv Namesrv, onMsg chan *peer.ServerMessage) *cluster {
	lst := &peer.ServerListener{
		OnMsg:        onMsg,
		OnDisconnect: make(chan *peer.Server, 1),
	}
	bucket := &cluster{
		gwid:    id,
		namesrv: namesrv,
		servers: make(map[ksuid.KSUID]*peer.Server, 10),
		lst:     lst,
		log:     log.WithField("gatewayId", id),
	}

	return bucket
}

func (c *cluster) start() {
	if !atomic.CompareAndSwapInt32(&c.started, 0, 1) {
		return
	}

	c.init()
	go c.handleConnectionClose()
}

func (c *cluster) init() {
	servers, err := c.namesrv.GetServers()
	if err != nil {
		c.log.Errorln(err)
		return
	}
	for _, sr := range servers {
		// try to connecting server
		c.connectServer(sr)
	}
}

// remove a logic server , 1.close connection 2.remove from bucket
func (c *cluster) disconnectServer(sid ksuid.KSUID) {
	s := c.delServer(sid)
	if s != nil {
		s.Close()
	}
}

// add a new server to this bucket
func (c *cluster) connectServer(sr wire.LgServer) {
	for i := 0; i < 5; i++ {
		c.log.Infof("try to connecting server %s : %s:%d", sr.Id, sr.Ip, sr.Port)

		p, err := buildServer(c.gwid, sr, c.lst)
		if err != nil {
			time.Sleep(time.Second * 5)
			c.log.Warn(err)
			continue
		}

		serverTotal.WithLabelValues(c.gwid.String()).Inc()
		c.addServer(p)

		return
	}
}

func buildServer(gwID ksuid.KSUID, sr wire.LgServer, lst *peer.ServerListener) (*peer.Server, error) {
	addr := fmt.Sprintf("%s:%d", sr.Ip, sr.Port)
	conn, err := peer.ConnectServer(addr, gwID, []byte(TypeGate))
	if err != nil {
		return nil, err
	}
	lgid, _ := ksuid.Parse(sr.Id)
	p := peer.NewServer(lgid, []byte(TypeGate), lst)
	p.Addr = addr
	_ = p.Start(conn, peer.SideClient)
	return p, nil
}

func (c *cluster) exit() {

}

func (c *cluster) handleConnectionClose() {
	for p := range c.lst.OnDisconnect { //remove disconnected logic server
		c.delServer(p.ID)
		c.log.Infof("logicServer[%s] disconnect", p.ID)
		serverTotal.WithLabelValues(c.gwid.String()).Dec()
		c.reconnect(p)
	}
}

// try reconnect if server is accident closed,otherwise return
func (c *cluster) reconnect(p *peer.Server) {
	tk := time.NewTicker(time.Second * 5)
	t1 := time.Now()
	for range tk.C {
		// maybe server has started
		if _, has := c.servers[p.ID]; has {
			return
		}
		//TODO: checking the server's available in remote registry

		err := p.Restart()
		if err != nil {
			if time.Since(t1) > time.Minute*30 {
				return
			}
			c.log.Warn(err)
			time.Sleep(time.Second * 5)
			continue
		}
		c.addServer(p)
		return
	}
}

// Get this function decide which server be returned with a load balance algorithm .
// using Round Robin for now , but in the further will using a better algorithm
// 1. Round
// 2. hash slot
func (c *cluster) Get(pid wire.PID) *peer.Server {
	c.RLock()
	defer c.RUnlock()
	slen := len(c.servers)
	if slen == 0 {
		c.log.Warn("servers is empty")
		return nil
	}

	c.index++
	c.index = (c.index) % slen
	return c.servers[c.ids[c.index]]
}

func (c *cluster) addServer(p *peer.Server) {
	c.Lock()
	defer c.Unlock()
	c.servers[p.ID] = p
	c.rebuildIds()
}

func (c *cluster) delServer(id ksuid.KSUID) *peer.Server {
	c.Lock()
	defer c.Unlock()
	server, ok := c.servers[id]
	if !ok {
		return nil
	}
	delete(c.servers, id)
	c.rebuildIds()
	return server
}

func (c *cluster) rebuildIds() {
	c.ids = make([]ksuid.KSUID, len(c.servers))
	i := 0
	for key := range c.servers {
		c.ids[i] = key
		i++
	}
}
