package peer

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	log "github.com/sirupsen/logrus"
)

func TestStartServer(t *testing.T) {
	ln, err := net.Listen("tcp", ":8088")
	if err != nil {
		// handle error
		t.Error(err)
		return
	}
	defer ln.Close()

	log.SetLevel(log.TraceLevel)

	lst := &ServerListener{
		OnMsg:        make(chan *ServerMessage, 1),
		OnDisconnect: make(chan *Server, 1),
	}
	log.SetLevel(log.DebugLevel)

	log.Info("stat listen")

	handshake := Handshake{
		OnHandshake: func(req HandshakeReq) (uint16, error) {
			return CodeHandshakeOk, nil
		},
	}
	quit := NewEvent()
	conf := &ServerConf{
		WriteWait:  10 * time.Second,
		PongWait:   5 * time.Second,
		PingPeriod: 2 * time.Second,
	}
	go func() {
		for {
			if quit.HasFired() {
				return
			}
			conn, err := ln.Accept()
			if err != nil {
				log.Error(err)
				return
			}
			req, err := handshake.Upgrade(conn)
			if err != nil {
				log.Error(err)
				conn.Close()
				continue
			}

			log.Info("new connect")
			server := NewServer(req.SID, nil, lst)
			server.SetConf(conf)
			_ = server.Start(conn, SideServer)
			go func() {
				handleLst(server, lst)
				server.Close()
			}()
		}
	}()

	sid := ksuid.New()

	lst2 := &ServerListener{
		OnMsg:        make(chan *ServerMessage, 1),
		OnDisconnect: make(chan *Server, 1),
	}
	client := NewServer(sid, nil, lst2)
	client.SetConf(conf)

	conn, err := ConnectServer("localhost:8088", sid, nil)
	if err != nil {
		log.Error(err)
		return
	}
	if err = client.Start(conn, SideClient); err != nil {
		log.Error(err)
		return
	}

	go func() {
		handleLst(client, lst2)
		quit.Fire()
	}()
	msg := bytes.Repeat([]byte{'a'}, 100)
	for i := 0; i < 1; i++ {
		err = client.Push(msg)
		if err != nil {
			log.Error(err)
			return
		}
	}

	<-quit.Done()
	client.Close()
}

func handleLst(peer *Server, lst *ServerListener) {
	count := 0
	for {
		select {
		case msg := <-lst.OnMsg:
			count++
			err := peer.Push(msg.Payload)
			if err != nil {
				log.Error(err)
			}
			if count%10 == 0 {
				log.Infof("recv from %v count:%v", peer.Side, count)
			}
			if count == 5000 {
				return
			}
			time.Sleep(time.Millisecond * 10)
		case peer := <-lst.OnDisconnect:
			log.Infof("peer disconnect %s on %v", peer.ID.String(), peer.Side)
			return
		}
	}
}
