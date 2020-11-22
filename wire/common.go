// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wire

import (
	"encoding/binary"
	"errors"
	"io"
	"strconv"
)

var (
	// bigEndian is a convenience variable since binary.LittleEndian is
	// quite long.
	bigEndian = binary.BigEndian
)

var (
	// ErrAddrOverflow ErrAddrOverflow
	ErrAddrOverflow = errors.New("address is overflow")
	// ErrInvaildAddress ErrInvaildAddress
	ErrInvaildAddress = errors.New("address is invaild")
)

// ReadUint8 从 reader 中读取一个 uint8
func ReadUint8(r io.Reader) (uint8, error) {
	var bytes = make([]byte, 1)
	if _, err := io.ReadFull(r, bytes); err != nil {
		return 0, err
	}
	return uint8(bytes[0]), nil
}

// ReadUint32 从 reader 中读取一个 uint32
func ReadUint32(r io.Reader) (uint32, error) {
	var bytes = make([]byte, 4)
	if _, err := io.ReadFull(r, bytes); err != nil {
		return 0, err
	}
	return bigEndian.Uint32(bytes), nil
}

// ReadUint16 从 reader 中读取一个 uint16
func ReadUint16(r io.Reader) (uint16, error) {
	var bytes = make([]byte, 2)
	if _, err := io.ReadFull(r, bytes); err != nil {
		return 0, err
	}
	return bigEndian.Uint16(bytes), nil
}

// ReadUint64 从 reader 中读取一个 uint64
func ReadUint64(r io.Reader) (uint64, error) {
	var bytes = make([]byte, 8)
	if _, err := io.ReadFull(r, bytes); err != nil {
		return 0, err
	}
	return bigEndian.Uint64(bytes), nil
}

// ReadString 从 reader 中读取一个 string
func ReadString(r io.Reader) (string, error) {
	buf, err := ReadBytes(r)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// ReadBytes 从 reader 中读取一个 []byte, reader中前4byte 必须是[]byte 的长度
func ReadBytes(r io.Reader) ([]byte, error) {
	bufLen, err := ReadUint32(r)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, bufLen)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

//ReadFixedBytes 读取固定长度的字节
func ReadFixedBytes(len int, r io.Reader) ([]byte, error) {
	buf := make([]byte, len)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteUint8 写一个 uint8到 writer 中
func WriteUint8(w io.Writer, val uint8) error {
	buf := []byte{byte(val)}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	return nil
}

// WriteUint16 写一个 int16到 writer 中
func WriteUint16(w io.Writer, val uint16) error {
	buf := make([]byte, 2)
	bigEndian.PutUint16(buf, val)
	if _, err := w.Write(buf); err != nil {
		return err
	}
	return nil
}

// WriteUint32 写一个 int32到 writer 中
func WriteUint32(w io.Writer, val uint32) error {
	buf := make([]byte, 4)
	bigEndian.PutUint32(buf, val)
	if _, err := w.Write(buf); err != nil {
		return err
	}
	return nil
}

// WriteUint64 写一个 int64到 writer 中
func WriteUint64(w io.Writer, val uint64) error {
	buf := make([]byte, 8)
	bigEndian.PutUint64(buf, val)
	if _, err := w.Write(buf); err != nil {
		return err
	}
	return nil
}

// WriteString 写一个 string 到 writer 中
func WriteString(w io.Writer, str string) error {
	if err := WriteBytes(w, []byte(str)); err != nil {
		return err
	}
	return nil
}

// WriteBytes 写一个 buf []byte 到 writer 中
func WriteBytes(w io.Writer, buf []byte) error {
	bufLen := len(buf)

	if err := WriteUint32(w, uint32(bufLen)); err != nil {
		return err
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	return nil
}

// ErrSIDLengthWrong ErrSIDLengthWrong
var ErrSIDLengthWrong = errors.New("SID length is wrong. 8bytes")

type IMNodeType string

const (
	NodeWebsocketGateway = IMNodeType("WG")
	NodeHttpGateway      = IMNodeType("HG")
	NodeLogicServer      = IMNodeType("LS")
)

// SID server id type
// 2bits type 2bits areaID 4bits NodeID
type SID [8]byte

// NilSID NilSID
var NilSID = SID{0, 0, 0, 0, 0, 0, 0, 0}

// ParseSID Parse String
func ParseSID(val string) (SID, error) {
	var sid SID
	if len(val) != 8 {
		return sid, ErrSIDLengthWrong
	}
	copy(sid[:], []byte(val))
	return sid, nil
}

//SIDBytes Constructs a SID from a 8-byte binary representation
func SIDBytes(b []byte) (SID, error) {
	var sid SID

	if len(b) != 8 {
		return sid, ErrSIDLengthWrong
	}

	copy(sid[:], b)
	return sid, nil
}

func (s SID) Type() IMNodeType {
	return IMNodeType(s[0:2])
}

func (s SID) Area() uint16 {
	area, _ := strconv.Atoi(string(s[2:4]))
	return uint16(area)
}

func (s SID) NodeID() uint32 {
	nid, _ := strconv.Atoi(string(s[4:8]))
	return uint32(nid)
}

func (s SID) String() string {
	return string(s[:])
}

// Decode Decode
func (s *SID) Decode(r io.Reader) error {
	buf := make([]byte, 8)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return err
	}
	copy(s[:], buf)
	return nil
}

// Encode Encode
func (s *SID) Encode(w io.Writer) error {
	_, err := w.Write(s[:])
	if err != nil {
		return err
	}
	return nil
}
