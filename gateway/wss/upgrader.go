package wshub

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/klintcheng/fim/peer"
	"github.com/klintcheng/fim/wire"

	"github.com/gobwas/ws"
	log "github.com/sirupsen/logrus"
)

const (
	crlf          = "\r\n"
	colonAndSpace = ": "
	commaAndSpace = ", "
)

// err info
var (
	ErrTokenInvalid   = errors.New("invalid parameter token")
	ErrMaxClientLimit = errors.New("over max clients")
)

var wsHsHader = ws.HandshakeHeaderHTTP(http.Header{
	"X-Go-Version": []string{runtime.Version()},
})

func upgradeConn(h *Hub, conn net.Conn, lst *peer.ClientListener) error {
	if atomic.LoadInt32(&h.clientNum) > int32(h.conf.MaxConnections) {
		wr := bufio.NewWriterSize(conn, 200)
		defer wr.Flush()
		writeStatusText(wr, http.StatusServiceUnavailable)
		writeErrorText(wr, ErrMaxClientLimit)

		return ErrMaxClientLimit
	}

	var de, tk, pi, ve, ra, addr string

	set := func(key, value string) {
		switch strings.ToUpper(key) {
		case "DEVICE":
			de = value
		case "TOKEN":
			tk = value
		case "PING":
			pi = value
		case "VERSION":
			ve = value
		case "RATE":
			ra = value
		}
	}
	var attrs *peer.ClientAttrs
	var conf *peer.ClientConf
	u := ws.Upgrader{
		OnHost: func(host []byte) error {
			return nil
		},
		OnRequest: func(uribytes []byte) error {
			u, _ := url.Parse(string(uribytes))
			q := u.Query()
			if q.Get("token") == "" {
				return nil
			}
			set("device", q.Get("device"))
			set("token", q.Get("token"))
			set("ping", q.Get("ping"))
			set("version", q.Get("version"))
			return nil
		},
		OnHeader: func(key, value []byte) error {
			k := peer.BytesToString(key)
			v := peer.BytesToString(value)
			set(k, v)
			if IsAddr(k) {
				addr = v
			}
			return nil
		},
		OnBeforeUpgrade: func() (ws.HandshakeHeader, error) {
			var err error
			if addr == "" {
				addr = conn.RemoteAddr().String()
			}
			attrs, conf, err = parseParameters(h, addr, de, tk, pi, ve, ra)
			if err != nil {
				loginFailTotal.WithLabelValues(h.ID.String()).Inc()

				if err == ErrTokenInvalid {
					return nil, ws.RejectConnectionError(
						ws.RejectionStatus(http.StatusUnauthorized),
						ws.RejectionReason(err.Error()))
				}

				return nil, ws.RejectConnectionError(
					ws.RejectionStatus(http.StatusBadRequest),
					ws.RejectionReason(err.Error()))
			}
			return wsHsHader, nil
		},
	}

	_, err := u.Upgrade(conn)
	if err != nil {
		conn.Close()
		return err
	}
	log.WithField("func", "upgrade").Debug(attrs, conf)

	return registClient(h, conn, lst, attrs, conf)
}

var ipExp = regexp.MustCompile(string("\\:[0-9]+$"))

func getIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	return ipExp.ReplaceAllString(remoteAddr, "")
}

func websocketHandler(h *Hub, w http.ResponseWriter, r *http.Request, lst *peer.ClientListener) {
	de := getFromHTTP(r, "device")
	tk := getFromHTTP(r, "token")
	pi := getFromHTTP(r, "ping")
	ve := getFromHTTP(r, "version")
	ra := r.Header.Get("rate")

	attrs, conf, err := parseParameters(h, r.RemoteAddr, de, tk, pi, ve, ra)
	if err != nil {
		loginFailTotal.WithLabelValues(h.ID.String()).Inc()

		if err == ErrTokenInvalid {
			resp(w, http.StatusBadRequest, err.Error())
			return
		}

		resp(w, http.StatusBadRequest, err.Error())
		return
	}

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		resp(w, http.StatusBadRequest, err.Error())
		return
	}

	err = registClient(h, conn, lst, attrs, conf)
	if err != nil {
		resp(w, http.StatusInternalServerError, err.Error())
		return
	}
}

func registClient(h *Hub, conn net.Conn, lst *peer.ClientListener, attr *peer.ClientAttrs, conf *peer.ClientConf) error {
	client := peer.NewClient(conn, lst, conf, attr)

	err := client.Start()
	if err != nil {
		return err
	}
	// push to hub
	err = h.Push(&Packet{useForConnect, client})
	if err != nil {
		return err
	}
	return nil
}

func parseParameters(h *Hub, rAddr, de, tk, pi, ve, ra string) (attrs *peer.ClientAttrs, conf *peer.ClientConf, err error) {
	if tk == "" {
		return nil, nil, ErrTokenInvalid
	}

	jwt, err := h.parseToken(tk)
	if err != nil {
		return nil, nil, ErrTokenInvalid
	}

	if de == "" || pi == "" {
		return nil, nil, fmt.Errorf("parameters is not allowed empty")
	}
	device := wire.DeviceType_value[de]
	v, err := version.NewVersion(ve)

	attrs = &peer.ClientAttrs{
		Device:   uint8(device),
		Username: jwt.Username,
		RemoteIP: getIP(rAddr),
		Version:  v,
	}
	ping, _ := strconv.Atoi(pi)
	if ping < 20 || ping > 120 {
		return nil, nil, fmt.Errorf("invalid parameter ping")
	}

	rate := int(peer.DefConf.Rate)
	if ra != "" {
		rate, _ = strconv.Atoi(ra)
	}
	conf = &peer.ClientConf{
		RemotePing:       true,
		PongWait:         time.Duration(ping*2)*time.Second + time.Second*5,
		ReaderBufferSize: peer.DefConf.ReaderBufferSize,
		Rate:             int64(rate),
	}
	return
}

func getFromHTTP(r *http.Request, key string) string {
	val := r.Header.Get(key)
	if val == "" {
		val = r.URL.Query().Get(key)
	}
	return val
}

func resp(w http.ResponseWriter, code int, body string) {
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func writeStatusText(bw *bufio.Writer, code int) {
	bw.WriteString("HTTP/1.1 ")
	bw.WriteString(strconv.Itoa(code))
	bw.WriteByte(' ')
	bw.WriteString(http.StatusText(code))
	bw.WriteString(crlf)
	bw.WriteString("Content-Type: text/plain; charset=utf-8")
	bw.WriteString(crlf)
}

func writeErrorText(bw *bufio.Writer, err error) {
	body := err.Error()
	bw.WriteString("Content-Length: ")
	bw.WriteString(strconv.Itoa(len(body)))
	bw.WriteString(crlf)
	bw.WriteString(crlf)
	bw.WriteString(body)
}

var ngAddr = map[string]bool{
	"X-Real-IP":       true,
	"X-Forwarded-For": true,
}

// IsAddr IsAddr
func IsAddr(key string) bool {
	_, ok := ngAddr[key]
	return ok
}
