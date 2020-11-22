package peer

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/pool/pbufio"
	"github.com/klintcheng/fim/wire"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

const (
	// state
	serverClosed = iota
	serverStarting
	serverStarted
	serverClosing
)

// OpCode OpCode
type OpCode byte

// op
const (
	OpHandshake = OpCode(iota + 1)
	OpHandshakeAck
	OpPing
	OpPong
	OpMessage
	OpClose
)

// Side Side
type Side byte

// Side define a peer
const (
	SideClient = Side(iota)
	SideServer
)

// Handshake code
const (
	CodeHandshakeOk            = uint16(200)
	CodeHandshakeSIDNull       = uint16(301)
	CodeHandshakeSIDInvaild    = uint16(302)
	CodeHandshakeDuplicatedSID = uint16(303)
)

const (
	defReadBufferSize  = 1024 * 16 // 16kb read buffer
	defWriteBufferSize = 1024 * 8
)

// Encoder Encoder
type Encoder interface {
	Encode(w io.Writer) (err error)
}

//Header tcp header
type Header [5]byte

// Decode Decode reader to Header
func (h *Header) Decode(r io.Reader) error {
	_, err := io.ReadFull(r, h[0:])
	return err
}

// Encode Encode Header to writer
func (h *Header) Encode(w io.Writer) error {
	_, err := w.Write(h[0:])
	return err
}

// NewHeader NewHeader
func NewHeader(opCode OpCode, plen uint32) Header {
	h := Header{byte(opCode), 0, 0, 0, 0}
	h.SetPayloadLen(plen)
	return h
}

// GetOpCode  GetOpCode
func (h *Header) GetOpCode() OpCode {
	return OpCode(h[0])
}

//GetPayloadLen GetPayloadLen
func (h *Header) GetPayloadLen() uint32 {
	return binary.BigEndian.Uint32(h[1:5])
}

//SetOpCode SetOpCode
func (h *Header) SetOpCode(opCode byte) {
	h[0] = opCode
}

//SetPayloadLen SetPayloadLen
func (h *Header) SetPayloadLen(len uint32) {
	binary.BigEndian.PutUint32(h[1:5], len)
}

// Frame Frame
type Frame struct {
	Header  Header
	Payload []byte
}

// NewFrame NewFrame
func NewFrame(opCode OpCode, payload []byte) *Frame {
	header := NewHeader(opCode, uint32(len(payload)))
	return &Frame{
		Header:  header,
		Payload: payload,
	}
}

// NewFrameWithEncoder NewFrameWithEncoder
func NewFrameWithEncoder(opCode OpCode, encoder Encoder) (*Frame, error) {
	buf := new(bytes.Buffer)
	if err := encoder.Encode(buf); err != nil {
		return nil, err
	}
	payload := buf.Bytes()
	header := NewHeader(opCode, uint32(len(payload)))
	return &Frame{
		Header:  header,
		Payload: payload,
	}, nil
}

// Decode Decode reader to Header
func (f *Frame) Decode(r io.Reader) (err error) {
	if err = f.Header.Decode(r); err != nil {
		return
	}
	plen := f.Header.GetPayloadLen()
	if plen == 0 {
		return
	}
	if f.Payload, err = wire.ReadFixedBytes(int(plen), r); err != nil {
		return
	}
	return
}

// Encode Encode Header to writer
func (f *Frame) Encode(w io.Writer) (err error) {
	if err = f.Header.Encode(w); err != nil {
		return
	}
	if _, err = w.Write(f.Payload); err != nil {
		return
	}
	return
}

func (f *Frame) mustGetHandshakeReq() (*HandshakeReq, error) {
	if f.Header.GetOpCode() != OpHandshake {
		return nil, fmt.Errorf("not Handshake")
	}
	handshake := new(HandshakeReq)
	if err := handshake.Decode(bytes.NewBuffer(f.Payload)); err != nil {
		return nil, err
	}
	return handshake, nil
}

func (f *Frame) mustGetHandshakeAck() (*HandshakeAck, error) {
	if f.Header.GetOpCode() != OpHandshakeAck {
		return nil, fmt.Errorf("not HandshakeAck")
	}
	handshakeAck := new(HandshakeAck)
	if err := handshakeAck.Decode(bytes.NewBuffer(f.Payload)); err != nil {
		return nil, err
	}
	return handshakeAck, nil
}

// WritePayload WritePayload
func (f *Frame) WritePayload(body Encoder) (err error) {
	buf := new(bytes.Buffer)
	if err = body.Encode(buf); err != nil {
		return
	}
	f.Payload = buf.Bytes()
	f.Header.SetPayloadLen(uint32(len(f.Payload)))
	return
}

// Error for client
var (
	ErrPeerIsClosed    = errors.New("connection is closed")
	ErrPeerIsOpened    = errors.New("connection is opened")
	ErrNotHandshake    = errors.New("not a handshake message")
	ErrHandshakeFailed = errors.New("handshake failed")
)

// ServerMessage ServerMessage
type ServerMessage struct {
	ID      ksuid.KSUID
	Payload []byte
}

// ServerConf 节点配置
type ServerConf struct {
	// Time allowed to write a message to the peer.
	WriteWait time.Duration
	// Time allowed to read the next pong message from the peer.
	PongWait   time.Duration
	PingPeriod time.Duration
}

// ServerListener ServerListener
type ServerListener struct {
	OnMsg        chan *ServerMessage
	OnDisconnect chan *Server
}

// Server 节点封装了 tcp 通信底层接口
type Server struct {
	sync.Mutex
	ID   ksuid.KSUID
	Meta []byte
	Addr string
	conf *ServerConf
	conn net.Conn
	log  *log.Entry
	// for outside
	// write chan *Frame
	onMsg        chan<- *ServerMessage
	onDisconnect chan<- *Server
	Side         Side
	state        int32
}

var defServerConf = &ServerConf{
	WriteWait:  10 * time.Second,
	PongWait:   35 * time.Second,
	PingPeriod: 15 * time.Second,
}

// NewServer NewServer
func NewServer(ID ksuid.KSUID, meta []byte, lst *ServerListener) *Server {

	return &Server{
		ID:           ID,
		Meta:         meta,
		conf:         defServerConf,
		conn:         nil,
		onMsg:        lst.OnMsg,
		onDisconnect: lst.OnDisconnect,
		state:        serverClosed,
		// write:        make(chan *Frame, 100),
		log: log.WithFields(log.Fields{
			"Loc":  "tcp_peer",
			"SID":  ID,
			"UUID": ksuid.New().String(),
		}),
	}
}

// Start connection , start
func (p *Server) Start(conn net.Conn, side Side) error {
	// Already state?
	if !atomic.CompareAndSwapInt32(&p.state, serverClosed, serverStarting) {
		return ErrPeerIsOpened
	}

	if conn == nil {
		atomic.CompareAndSwapInt32(&p.state, serverStarting, serverClosed)
		return fmt.Errorf("no connection")
	}
	p.conn = conn
	p.Side = side
	wait := new(sync.WaitGroup)
	wait.Add(1)
	go p.readHandler(wait)

	// wait.Add(1)
	// go p.writeHandler(wait)

	if p.Side == SideClient {
		wait.Add(1)
		go p.heartbeat(wait)
	}
	wait.Wait()

	p.log.Debug("started")

	if !atomic.CompareAndSwapInt32(&p.state, serverStarting, serverStarted) {
		return ErrPeerIsOpened
	}
	return nil
}

// Restart Restart
func (p *Server) Restart() error {
	if p.Addr == "" {
		return fmt.Errorf("Addr is nil")
	}
	atomic.StoreInt32(&p.state, serverClosed)

	conn, err := ConnectServer(p.Addr, p.ID, p.Meta)
	if err != nil {
		return err
	}

	return p.Start(conn, p.Side)
}

// ConnectServer Connect to server
// sid is self serverID
// gatewayID G01 --> logicServer L01 , sid = G01
func ConnectServer(serverURL string, clientID ksuid.KSUID, meta []byte) (conn net.Conn, err error) {
	conn, err = net.DialTimeout("tcp", serverURL, time.Second*10)
	if err != nil {
		return
	}
	req, err := NewFrameWithEncoder(OpHandshake, &HandshakeReq{SID: clientID, Meta: meta})
	if err != nil {
		return
	}
	// write handshake to server
	if err = req.Encode(conn); err != nil {
		return
	}
	// wait for handshake ack
	resp := Frame{}
	if err = resp.Decode(conn); err != nil {
		return
	}
	// the first frame must be HandshakeAck
	ack, err := resp.mustGetHandshakeAck()
	if err != nil {
		return
	}
	if ack.Code != 200 {
		err = fmt.Errorf("Handshake Failed, Ack code: %v, reason :%s", ack.Code, ack.Error)
		return
	}
	return
}

// func (p *Server) writeHandler(wait *sync.WaitGroup) {
// 	p.log.Info("writeHandler start")

// 	defer func() {
// 		p.log.Info("writeHandler exit")
// 	}()

// 	writer := bufio.NewWriterSize(p.conn, defWriteBufferSize)
// 	wait.Done()

// 	for {
// 		select {
// 		case frame := <-p.write:
// 			p.Lock()

// 			err := p.conn.SetWriteDeadline(time.Now().Add(p.conf.WriteWait))
// 			if err != nil {
// 				p.Unlock()
// 				return
// 			}
// 			err = frame.Encode(writer)
// 			if err != nil {
// 				p.Unlock()
// 				return
// 			}

// 			bufLen := len(p.write)
// 			for i := 0; i < bufLen; i++ {
// 				err = frame.Encode(writer)
// 				if err != nil {
// 					p.Unlock()
// 					return
// 				}
// 			}
// 			if err = writer.Flush(); err != nil {
// 				p.Unlock()
// 				return
// 			}
// 			p.Unlock()
// 		case <-p.quit.Done():
// 			break
// 		}
// 	}
// }

func (p *Server) heartbeat(wait *sync.WaitGroup) {
	ticker := time.NewTicker(p.conf.PingPeriod)
	defer func() {
		ticker.Stop()
		p.log.Info("heartbeat exit")
	}()
	wait.Done()

	for range ticker.C {
		// only client side send ping
		err := p.WriteControl(OpPing, nil)
		if err != nil {
			break
		}
		p.log.Tracef("send ping to %s", p.Addr)
	}
}

func (p *Server) readHandler(wait *sync.WaitGroup) {
	p.log.Info("readHandler start")
	defer func() {
		p.log.Info("readHandler exit")

		if !atomic.CompareAndSwapInt32(&p.state, serverStarted, serverClosing) {
			return
		}

		_ = p.conn.Close()
		p.onDisconnect <- p

		atomic.CompareAndSwapInt32(&p.state, serverClosing, serverClosed)
	}()
	r := bufio.NewReaderSize(p.conn, defReadBufferSize)
	var header Header
	var payloadLen uint32

	if p.Side == SideServer {
		_ = p.conn.SetReadDeadline(time.Now().Add(p.conf.PongWait))
	}
	wait.Done()

	for {
		err := header.Decode(r)
		if err != nil {
			p.log.Warn(err)
			break
		}
		if header.GetOpCode() == OpClose {
			p.log.Debug("server close")
			break
		} else if header.GetOpCode() == OpPing {
			// ack a pong frame ,otherwise the other side maybe close connection
			err = p.WriteControl(OpPong, nil)
			if err != nil {
				p.log.Debug(err)
				break
			}
			// reset pong wait time
			_ = p.conn.SetReadDeadline(time.Now().Add(p.conf.PongWait))
			p.log.Tracef("received ping from %s, ack with a pong", p.Addr)
			continue
		} else if header.GetOpCode() == OpPong {
			p.log.Tracef("received pong from %s", p.Addr)
			// read pong from server side
			continue
		}

		payloadLen = header.GetPayloadLen()
		if payloadLen == 0 {
			continue
		}

		payload := make([]byte, payloadLen)
		_, err = io.ReadFull(r, payload)
		if err != nil {
			p.log.Debug("read err: ", err)
			break
		}

		p.onMsg <- &ServerMessage{
			ID:      p.ID,
			Payload: payload,
		}
	}
}

// WriteControl WriteControl
func (p *Server) WriteControl(opCode OpCode, payload []byte) (err error) {
	p.Lock()
	defer p.Unlock()
	err = p.conn.SetWriteDeadline(time.Now().Add(p.conf.WriteWait))
	if err != nil {
		return
	}
	frame := NewFrame(opCode, payload)
	err = frame.Encode(p.conn)
	return
}

func (p *Server) writeFrame(frame *Frame) (err error) {
	p.Lock()
	defer p.Unlock()
	err = p.conn.SetWriteDeadline(time.Now().Add(p.conf.WriteWait))
	if err != nil {
		return
	}
	bw := pbufio.GetWriter(p.conn, defWriteBufferSize)
	defer func() {
		pbufio.PutWriter(bw)
	}()

	err = frame.Encode(bw)
	if err != nil {
		return
	}
	if err = bw.Flush(); err != nil {
		p.log.Debug(err)
		return
	}
	return
}

// Push push message to peer
func (p *Server) Push(payload []byte) error {
	if atomic.LoadInt32(&p.state) != serverStarted {
		return ErrPeerIsClosed
	}

	// p.write <- NewFrame(OpMessage, payload)
	return p.writeFrame(NewFrame(OpMessage, payload))
}

// Close Close peer connection
func (p *Server) Close() {
	if !atomic.CompareAndSwapInt32(&p.state, serverStarted, serverClosing) {
		return
	}
	defer atomic.CompareAndSwapInt32(&p.state, serverClosing, serverClosed)

	_ = p.WriteControl(OpClose, nil)
	_ = p.conn.Close()
}

// Handshake Handshake
type Handshake struct {
	OnHandshake func(req HandshakeReq) (uint16, error)
}

// Upgrade finish a Handshake
func (h *Handshake) Upgrade(conn net.Conn) (req *HandshakeReq, err error) {
	code := CodeHandshakeOk
	var validErr error
	defer func() {
		ack := HandshakeAck{Code: code}
		if validErr != nil {
			ack.Error = validErr.Error()
		}

		resp, err := NewFrameWithEncoder(OpHandshakeAck, &ack)
		if err != nil {
			return
		}
		if err = resp.Encode(conn); err != nil {
			return
		}
	}()
	// read first frame as a handshake
	frame := Frame{}
	if err = frame.Decode(conn); err != nil {
		return
	}
	// get payload
	req, err = frame.mustGetHandshakeReq()
	if err != nil {
		return
	}

	code, validErr = h.OnHandshake(*req)
	return
}

//HandshakeReq HandshakeReq
type HandshakeReq struct {
	SID  ksuid.KSUID
	Meta []byte
}

//Decode Decode reader to Header
func (h *HandshakeReq) Decode(r io.Reader) (err error) {
	if h.SID, err = wire.DecodeKSUID(r); err != nil {
		return err
	}
	if h.Meta, err = wire.ReadBytes(r); err != nil {
		return err
	}
	return nil
}

// Encode Encode Header to writer
func (h *HandshakeReq) Encode(w io.Writer) (err error) {
	if _, err = w.Write(h.SID.Bytes()); err != nil {
		return err
	}
	if err := wire.WriteBytes(w, h.Meta); err != nil {
		return err
	}
	return nil
}

// HandshakeAck HandshakeAck
type HandshakeAck struct {
	Code  uint16
	Error string
}

// Decode Decode reader to Header
func (h *HandshakeAck) Decode(r io.Reader) (err error) {
	if h.Code, err = wire.ReadUint16(r); err != nil {
		return err
	}
	if h.Error, err = wire.ReadString(r); err != nil {
		return err
	}
	return err
}

// Encode Encode Header to writer
func (h *HandshakeAck) Encode(w io.Writer) (err error) {
	if err = wire.WriteUint16(w, h.Code); err != nil {
		return err
	}
	if err = wire.WriteString(w, h.Error); err != nil {
		return err
	}
	return err
}

// SetConf SetConf
func (p *Server) SetConf(conf *ServerConf) {
	p.conf = conf
}
