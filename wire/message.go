package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/golang/protobuf/proto"
	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

// ErrPIDNoFound ErrPIDNoFound
var ErrPIDNoFound = errors.New("protocolID no found")

// PID PID
type PID uint8

// AckCode im AckCode
type AckCode uint8

func (p PID) String() string {
	name, has := ProtocolMap[p]
	if has {
		return fmt.Sprintf("%v_%v", byte(p), name)
	}
	return fmt.Sprintf("%v", byte(p))
}

func (a AckCode) String() string {
	var str string
	switch a {
	case AckCodeOk:
		str = "Ok(0x00)"
	case AckCodeReceiverOffline:
		str = "ReceiverOffline(0x01)"
	case AckCodeBeRejected:
		str = "BeRejected(0x10)"
	case AckCodeGroupBeMuted:
		str = "GroupBeMuted(0x11)"
	case AckCodeGroupNoFound:
		str = "GroupNoFound(0x12)"
	case AckCodeHitSensitiveWord:
		str = "HitSensitiveWord(0x13)"
	case AckCodeServerErr:
		str = "ServerErr(0x50)"
	case AckCodeSessionLost:
		str = "SessionLost(0x51)"
	}
	return str
}

// common ack code for reqeust
const (
	AckCodeOk               = AckCode(0x00) // ok
	AckCodeReceiverOffline  = AckCode(0x01) // the receiver is offline
	AckCodeBeRejected       = AckCode(0x10) // message be rejected
	AckCodeGroupBeMuted     = AckCode(0x11) // the group was muted
	AckCodeGroupNoFound     = AckCode(0x12) // group no found
	AckCodeHitSensitiveWord = AckCode(0x13) // hit sensitive word
	AckCodeServerErr        = AckCode(0x50) // server error
	AckCodeSessionLost      = AckCode(0x51) // session lost
)

// PID defined data type between client and server
const (
	PIDLogin            = PID(0x01)
	PIDLoginACK         = PID(0x02)
	PIDKickOut          = PID(0x03)
	PIDLogout           = PID(0x04)
	PIDPing             = PID(0x05)
	PIDPong             = PID(0x06)
	PIDChat             = PID(0x11)
	PIDChatAck          = PID(0x12)
	PIDChatPush         = PID(0x13)
	PIDChatPushAck      = PID(0x14)
	PIDChatSync         = PID(0x15)
	PIDChatGroup        = PID(0x16)
	PIDChatGroupAck     = PID(0x17)
	PIDChatGroupPush    = PID(0x18)
	PIDChatGroupPushAck = PID(0x19)
)

// ProtocolMap defined PID to Name
var ProtocolMap = map[PID]string{
	PIDLogin:            "Login",
	PIDLoginACK:         "LoginACK",
	PIDKickOut:          "KickOut",
	PIDLogout:           "Logout",
	PIDPing:             "Ping",
	PIDPong:             "Pong",
	PIDChat:             "Chat",
	PIDChatAck:          "ChatAck",
	PIDChatPush:         "ChatPush",
	PIDChatPushAck:      "PushAck",
	PIDChatSync:         "ChatSync",
	PIDChatGroup:        "ChatGroupReq",
	PIDChatGroupAck:     "ChatGroupAck",
	PIDChatGroupPush:    "ChatGroupPush",
	PIDChatGroupPushAck: "ChatGroupPushAck",
}

// IsProtocol IsProtocol
func IsProtocol(id PID) bool {
	_, has := ProtocolMap[id]
	return has
}

// Header 定义了网关对外的client协议头
type Header [10]byte

// GetPID  GetPID
func (h *Header) GetPID() PID {
	return PID(h[0])
}

//GetSequence GetSequence
func (h *Header) GetSequence() uint16 {
	return uint16(bigEndian.Uint16(h[1:3]))
}

//SetSequence SetSequence
func (h *Header) SetSequence(seq uint16) {
	bigEndian.PutUint16(h[1:3], seq)
}

//GetSequenceAck GetSequenceAck
func (h *Header) GetSequenceAck() uint16 {
	return uint16(bigEndian.Uint16(h[3:5]))
}

//SetSequenceAck SetSequenceAck
func (h *Header) SetSequenceAck(ack uint16) {
	bigEndian.PutUint16(h[3:5], ack)
}

// GetPayloadLen GetPayloadLen
func (h *Header) GetPayloadLen() uint16 {
	return bigEndian.Uint16(h[5:7])
}

// SetPayloadLen SetPayloadLen
func (h *Header) SetPayloadLen(len uint16) {
	bigEndian.PutUint16(h[5:7], len)
}

// GetAckCode  GetAckCode
func (h *Header) GetAckCode() AckCode {
	return AckCode(h[9])
}

// Decode Decode reader to Header
func (h *Header) Decode(r io.Reader) error {
	_, err := r.Read(h[0:])
	return err
}

// Encode Encode Header to writer
func (h *Header) Encode(w io.Writer) error {
	_, err := w.Write(h[0:])
	return err
}

func (h Header) String() string {
	return fmt.Sprintf("PID:%v,Seq:%v,SeqAck:%v,AckCode:%v,Len:%v", h.GetPID(), h.GetSequence(), h.GetSequenceAck(), h.GetAckCode(), h.GetPayloadLen())
}

// Message 定义了网关对外的client消息结构
type Message struct {
	Header  Header
	Payload []byte
}

// NewMessage new a empty payload message
func NewMessage(protocolID PID, sequence uint16) *Message {
	header := Header{}
	header[0] = byte(protocolID)
	bigEndian.PutUint16(header[1:3], sequence)
	return &Message{
		Header: header,
	}
}

// NewMessage new a empty payload message
func NewErrMessage(protocolID PID, sequence uint16, errCode AckCode) *Message {
	header := Header{}
	header[0] = byte(protocolID)
	header[9] = byte(errCode)
	bigEndian.PutUint16(header[1:3], sequence)
	return &Message{
		Header: header,
	}
}

// Decode Decode reader to Message
func (m *Message) Decode(r io.Reader) error {
	err := m.Header.Decode(r)
	if err != nil {
		return err
	}
	if m.Header.GetPayloadLen() == 0 {
		return nil
	}
	m.Payload = make([]byte, m.Header.GetPayloadLen())
	_, err = r.Read(m.Payload)
	if err != nil {
		return err
	}
	return err
}

// Encode Encode Header to writer
func (m *Message) Encode(w io.Writer) error {
	err := m.Header.Encode(w)
	if err != nil {
		return err
	}
	if len(m.Payload) == 0 {
		return nil
	}
	_, err = w.Write(m.Payload)
	if err != nil {
		return err
	}
	return err
}

// Bytes Bytes
func (m *Message) Bytes() []byte {
	buf := new(bytes.Buffer)
	err := m.Encode(buf)
	if err != nil {
		log.Error(err.Error())
	}
	return buf.Bytes()
}

func (m *Message) Write(payload proto.Message) error {
	out, err := proto.Marshal(payload)
	if err != nil {
		return err
	}
	m.Payload = out
	m.Header.SetPayloadLen(uint16(len(out)))
	return nil
}

// GetPayloadEntity GetPayloadEntity
func (m *Message) GetPayloadEntity() (interface{}, error) {
	message, err := GetEmptyMessage(m.Header.GetPID())
	if err != nil {
		return nil, err
	}
	err = proto.Unmarshal(m.Payload, message.(proto.Message))
	if err != nil {
		return nil, err
	}
	return message, nil
}

// GetEmptyMessage GetEmptyMessage
func GetEmptyMessage(protocolID PID) (interface{}, error) {
	switch protocolID {
	case PIDLogin:
		return new(LoginReq), nil
	case PIDLoginACK:
		return new(LoginAck), nil
	case PIDKickOut:
		return new(KickOut), nil
	case PIDLogout:
		return new(LogoutReq), nil
	case PIDChatGroup:
		fallthrough
	case PIDChat: // same with upper
		return new(ChatReq), nil
	case PIDChatGroupAck:
		fallthrough
	case PIDChatAck: // same with upper
		return new(ChatAck), nil
	case PIDChatGroupPush:
		fallthrough
	case PIDChatPush: // same with upper
		return new(ChatPush), nil
	case PIDChatGroupPushAck:
		fallthrough
	case PIDChatPushAck: // same with upper
		return new(ChatPushAck), nil
	case PIDChatSync:
		return new(ChatSync), nil
	}
	return nil, ErrPIDNoFound
}

type str32 [32]byte

//RelayPacket RelayPacket
type RelayPacket struct {
	Uuid      ksuid.KSUID //sender uuid
	Username  string      //sender username
	Device    byte
	Location  ksuid.KSUID
	Receivers []ksuid.KSUID
	Payload   *Message
}

// ArrivalAts    []ksuid.KSUID // ksuid.KSUID of gateway that message arrival at
// ArrivalTimes  []uint64      //message arrival time at gateway

// NewEmptyRelayMessage NewEmptyRelayMessage
func NewEmptyRelayMessage(location ksuid.KSUID) *RelayPacket {
	return &RelayPacket{
		Location:  location,
		Receivers: nil,
	}
}

func NewRelayMessage(uuid ksuid.KSUID,
	username string, //sender username
	device byte,
	location ksuid.KSUID) *RelayPacket {
	return &RelayPacket{
		Uuid:      uuid,
		Username:  username,
		Device:    device,
		Location:  location,
		Receivers: nil,
	}
}

//Decode Decode RelayPacket
func (m *RelayPacket) Decode(r io.Reader) error {
	var err error
	// sender
	m.Uuid, err = DecodeKSUID(r)
	if err != nil {
		return err
	}
	m.Username, err = ReadString(r)
	if err != nil {
		return err
	}
	m.Device, err = ReadUint8(r)
	if err != nil {
		return err
	}
	// Location
	m.Location, err = DecodeKSUID(r)
	if err != nil {
		return err
	}
	// Receivers
	length, err := ReadUint32(r)
	if err != nil {
		return err
	}
	m.Receivers = make([]ksuid.KSUID, length)
	for i := uint32(0); i < length; i++ {
		m.Receivers[i], _ = DecodeKSUID(r)
	}

	// Payload
	m.Payload = new(Message)
	if err = m.Payload.Decode(r); err != nil {
		return err
	}
	return nil
}

func DecodeKSUID(r io.Reader) (ksuid.KSUID, error) {
	bytes, err := ReadFixedBytes(len(ksuid.Nil), r)
	if err != nil {
		return ksuid.Nil, err
	}
	id, err := ksuid.FromBytes(bytes)
	if err != nil {
		return ksuid.Nil, err
	}
	return id, nil
}

//Encode Encode RelayPacket
func (m *RelayPacket) Encode(w io.Writer) error {
	// Sender
	if _, err := w.Write(m.Uuid.Bytes()); err != nil {
		return err
	}
	if err := WriteString(w, m.Username); err != nil {
		return err
	}
	if err := WriteUint8(w, m.Device); err != nil {
		return err
	}
	if _, err := w.Write(m.Location.Bytes()); err != nil {
		return err
	}

	// Receivers
	if err := WriteUint32(w, uint32(len(m.Receivers))); err != nil {
		return err
	}
	for i := 0; i < len(m.Receivers); i++ {
		if _, err := w.Write(m.Receivers[i].Bytes()); err != nil {
			return err
		}
	}
	// Payload
	if err := m.Payload.Encode(w); err != nil {
		return err
	}
	return nil
}

// Bytes Bytes
func (m *RelayPacket) Bytes() []byte {
	buf := new(bytes.Buffer)
	err := m.Encode(buf)
	if err != nil {
		log.Error(err.Error())
	}
	return buf.Bytes()
}

// Ping Ping message with no payload
var Ping = []byte{byte(PIDPing), 0, 0, 0, 0, 0, 0, 0, 0, 0}

// Pong Pong message with no payload
var Pong = []byte{byte(PIDPong), 0, 0, 0, 0, 0, 0, 0, 0, 0}
