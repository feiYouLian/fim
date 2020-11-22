package peer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/imdario/mergo"
	"github.com/juju/ratelimit"
	"github.com/klintcheng/fim/wire"

	"github.com/hashicorp/go-version"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

const (
	// state
	clientClosed = iota
	clientStarted
	clientClosing
)

// Error for client
var (
	ErrClientIsClosed  = errors.New("connection is closed")
	ErrClientIsStarted = errors.New("connection is started")
)

const defWriteWait = time.Second * 5

// ClientMessage ClientMessage
type ClientMessage struct {
	ID      ksuid.KSUID
	Attrs   *ClientAttrs
	Payload *wire.Message
}

// ClientConf 节点配置
type ClientConf struct {
	// Time allowed to read the next message from the peer.
	PongWait         time.Duration
	RemotePing       bool
	ReaderBufferSize int
	// messages per seconds, if client send message
	Rate int64
}

// DefConf DefConf
var DefConf = ClientConf{
	PongWait:         time.Second * 60,
	RemotePing:       true,
	ReaderBufferSize: ws.DefaultServerReadBufferSize,
	Rate:             10,
}

// Reset Reset
func (c *ClientConf) Reset() {
	c.PongWait = 0
	c.RemotePing = false
	c.ReaderBufferSize = ws.DefaultServerReadBufferSize
	c.Rate = 10
}

// ClientListener Client Listener
type ClientListener struct {
	OnMsg        chan *ClientMessage
	OnDisconnect chan *WsClient
}

// ClientAttrs ClientAttrs
type ClientAttrs struct {
	Username string
	Device   uint8
	RemoteIP string
	Version  *version.Version
}

// WsClient 节点封装了 websocket 通信底层接口
type WsClient struct {
	io    sync.RWMutex
	ID    ksuid.KSUID
	Attrs *ClientAttrs
	conf  *ClientConf
	conn  net.Conn
	log   *log.Entry
	// for outside
	onMsg        chan<- *ClientMessage
	onDisconnect chan<- *WsClient
	state        int32 // 0 unconnected 1 state
}

// NewClient 创建一个新的节点,如果 conf 为空，会使用一个默认的配置
func NewClient(conn net.Conn, lst *ClientListener, conf *ClientConf, attrs *ClientAttrs) *WsClient {
	id, err := ksuid.NewRandom()
	if err != nil {
		log.Warn(err)
	}
	_ = mergo.Merge(&conf, DefConf)

	return &WsClient{
		ID:           id,
		Attrs:        attrs,
		conf:         conf,
		conn:         conn,
		onMsg:        lst.OnMsg,
		onDisconnect: lst.OnDisconnect,
		state:        clientClosed,
		log: log.WithFields(log.Fields{
			"ID":       id,
			"Username": attrs.Username,
			"Device":   attrs.Device,
			"Version":  attrs.Version.Original(),
		}),
	}
}

// Start connection , start
func (p *WsClient) Start() error {
	// Already state?
	if !atomic.CompareAndSwapInt32(&p.state, clientClosed, clientStarted) {
		return ErrClientIsStarted
	}

	go p.readHandler()

	p.log.Infof("peer start, from %s", p.Attrs.RemoteIP)
	return nil
}

func (p *WsClient) readHandler() {
	defer func() {
		if !atomic.CompareAndSwapInt32(&p.state, clientStarted, clientClosing) { //closing
			return
		}
		_ = p.conn.Close()
		p.onDisconnect <- p
		p.clean()
		defer atomic.CompareAndSwapInt32(&p.state, clientClosing, clientClosed)
	}()

	if p.conf.RemotePing {
		_ = p.conn.SetReadDeadline(time.Now().Add(p.conf.PongWait))
	}

	rate := ratelimit.NewBucketWithQuantum(time.Second, p.conf.Rate, p.conf.Rate)

	rd := bufio.NewReaderSize(p.conn, p.conf.ReaderBufferSize)
	for {
		header, err := ws.ReadHeader(rd)
		if err != nil {
			p.log.Debug(err.Error())
			break
		}
		// remote side will close connection
		if header.OpCode == ws.OpClose {
			break
		}
		// reset read deadline when received a payload with any opcode
		_ = p.conn.SetReadDeadline(time.Now().Add(p.conf.PongWait))

		if header.OpCode == ws.OpPing {
			p.log.Trace("received ping, Ack with pong ,PongWait:", p.conf.PongWait)
			err = p.WriteControl(ws.OpPong, nil)
			if err != nil {
				break
			}
			continue
		}
		if header.Length == 0 {
			continue
		}
		payload := make([]byte, header.Length)
		_, err = io.ReadFull(rd, payload)
		if err != nil {
			break
		}
		if rate.Take(1) > 0 {
			p.log.Warn("Rate Limit.Throw Message, payload: ", fmt.Sprint(payload[0:10]))
			continue
		}
		if header.Masked {
			ws.Cipher(payload, header.Mask, 0)
		}
		if header.OpCode == ws.OpBinary {
			p.handleMessage(payload)
		}

	}
}

func (p *WsClient) handleMessage(payload []byte) {
	buf := bytes.NewBuffer(payload)
	for {
		msg := new(wire.Message)
		if err := msg.Decode(buf); err != nil {
			if err != io.EOF {
				p.log.Error(err)
			}
			break
		}
		if msg.Header.GetPID() == wire.PIDPing {
			p.log.Trace("recv PIDPing")
			_ = p.writeMessage(wire.Pong)
			_ = p.conn.SetReadDeadline(time.Now().Add(p.conf.PongWait))
			continue
		}
		p.onMsg <- &ClientMessage{
			ID:      p.ID,
			Attrs:   p.Attrs,
			Payload: msg,
		}
	}
}

// WriteControl WriteControl
func (p *WsClient) WriteControl(opCode ws.OpCode, payload []byte) error {
	p.io.Lock()
	defer p.io.Unlock()
	err := p.conn.SetWriteDeadline(time.Now().Add(defWriteWait))
	if err != nil {
		return err
	}
	return wsutil.WriteServerMessage(p.conn, opCode, payload)
}

func (p *WsClient) writeMessage(payload []byte) error {
	p.io.Lock()
	defer p.io.Unlock()
	err := p.conn.SetWriteDeadline(time.Now().Add(defWriteWait))
	if err != nil {
		return err
	}
	return wsutil.WriteServerBinary(p.conn, payload)
}

// Push push message to peer
func (p *WsClient) Push(payload []byte) error {
	if atomic.LoadInt32(&p.state) != clientStarted {
		return ErrClientIsClosed
	}
	return p.writeMessage(payload)
}

// Close Close peer connection
func (p *WsClient) Close() {
	if !atomic.CompareAndSwapInt32(&p.state, clientStarted, clientClosing) { //closing
		return
	}
	defer atomic.CompareAndSwapInt32(&p.state, clientClosing, clientClosed)

	if err := p.WriteControl(ws.OpClose, nil); err != nil {
		p.log.Error(err.Error())
		return
	}
	_ = p.conn.Close()
}

func (p *WsClient) clean() {

	p.conn = nil
	p.Attrs = nil
}
