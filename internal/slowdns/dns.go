package slowdns

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"www.bamsoftware.com/git/dnstt.git/dns"
	"www.bamsoftware.com/git/dnstt.git/turbotunnel"
)

const (
	numPadding = 3

	numPaddingForPoll = 8

	initPollDelay       = 500 * time.Millisecond
	maxPollDelay        = 10 * time.Second
	pollDelayMultiplier = 2.0

	pollLimit = 16
)

var base32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type DNSPacketConn struct {
	clientID turbotunnel.ClientID
	domain   dns.Name
	qtype    uint16
	edns     uint16

	pollChan chan struct{}
	stats    Stats
	flight   *flight

	*turbotunnel.QueuePacketConn
}

const DefaultEDNS = 1400

func NewDNSPacketConn(transport net.PacketConn, addr net.Addr, domain dns.Name, qtype uint16, edns int) *DNSPacketConn {
	if qtype == 0 {
		qtype = dns.RRTypeTXT
	}
	if edns < 512 || edns > 4096 {
		edns = DefaultEDNS
	}

	clientID := turbotunnel.NewClientID()
	c := &DNSPacketConn{
		clientID:        clientID,
		domain:          domain,
		qtype:           qtype,
		edns:            uint16(edns),
		pollChan:        make(chan struct{}, pollLimit),
		flight:          newFlight(),
		QueuePacketConn: turbotunnel.NewQueuePacketConn(clientID, 0),
	}
	go func() {
		err := c.recvLoop(transport)
		if err != nil {
			log.Printf("recvLoop: %v", err)
		}
	}()
	go func() {
		err := c.sendLoop(transport, addr)
		if err != nil {
			log.Printf("sendLoop: %v", err)
		}
	}()
	return c
}

func dnsResponsePayload(resp *dns.Message, domain dns.Name, qtype uint16) []byte {
	p, _ := responsePayload(resp, domain, qtype)
	return p
}

func responsePayload(resp *dns.Message, domain dns.Name, qtype uint16) ([]byte, bool) {
	if resp.Flags&0x8000 != 0x8000 {

		return nil, false
	}
	if resp.Flags&0x000f != dns.RcodeNoError {
		return nil, false
	}

	if len(resp.Answer) < 1 {
		return nil, false
	}
	if isAddrType(qtype) {
		var datas [][]byte
		for _, a := range resp.Answer {
			if a.Type != qtype {
				continue
			}
			if _, ok := a.Name.TrimSuffix(domain); !ok {
				return nil, false
			}
			datas = append(datas, a.Data)
		}
		payload, err := decodeAddrAnswers(qtype, datas)
		if err != nil {
			return nil, true
		}
		return payload, false
	}
	answer := resp.Answer[0]
	for _, a := range resp.Answer {
		if a.Type == qtype {
			answer = a
			break
		}
	}

	_, ok := answer.Name.TrimSuffix(domain)
	if !ok {

		return nil, false
	}

	if answer.Type != qtype {
		return nil, false
	}
	payload, err := decodeRData(qtype, answer.Data, domain)
	if err != nil {
		return nil, true
	}

	return payload, false
}

func nextPacket(r *bytes.Reader) ([]byte, error) {
	for {
		var n uint16
		err := binary.Read(r, binary.BigEndian, &n)
		if err != nil {

			return nil, err
		}
		p := make([]byte, n)
		_, err = io.ReadFull(r, p)

		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return p, err
	}
}

func (c *DNSPacketConn) recvLoop(transport net.PacketConn) error {
	for {
		var buf [4096]byte
		n, addr, err := transport.ReadFrom(buf[:])
		if err != nil {
			return err
		}

		resp, err := dns.MessageFromWireFormat(buf[:n])
		if err != nil {
			log.Printf("MessageFromWireFormat: %v", err)
			continue
		}

		payload, broken := responsePayload(&resp, c.domain, c.qtype)
		c.stats.note(&resp, n, broken, payload)
		if c.qtype == RRTypeAAAA && len(payload) > 0 && synthesized(&resp) {
			c.stats.DNS64.Add(1)
		}
		c.flight.answered()
		c.stats.Cap.Store(int64(c.flight.limit()))

		r := bytes.NewReader(payload)
		any := false
		for {
			p, err := nextPacket(r)
			if err != nil {
				break
			}
			any = true
			c.QueuePacketConn.QueueIncoming(p, addr)
		}

		if any {
			select {
			case c.pollChan <- struct{}{}:
			default:
			}
		}
	}
}

func chunks(p []byte, n int) [][]byte {
	var result [][]byte
	for len(p) > 0 {
		sz := len(p)
		if sz > n {
			sz = n
		}
		result = append(result, p[:sz])
		p = p[sz:]
	}
	return result
}

func (c *DNSPacketConn) send(transport net.PacketConn, p []byte, addr net.Addr) error {
	var decoded []byte
	{
		if len(p) >= 224 {
			return fmt.Errorf("too long")
		}
		var buf bytes.Buffer

		buf.Write(c.clientID[:])
		n := numPadding
		if len(p) == 0 {
			n = numPaddingForPoll
		}

		buf.WriteByte(byte(224 + n))
		io.CopyN(&buf, rand.Reader, int64(n))

		if len(p) > 0 {
			buf.WriteByte(byte(len(p)))
			buf.Write(p)
		}
		decoded = buf.Bytes()
	}

	encoded := make([]byte, base32Encoding.EncodedLen(len(decoded)))
	base32Encoding.Encode(encoded, decoded)
	encoded = bytes.ToLower(encoded)
	labels := chunks(encoded, 63)
	labels = append(labels, c.domain...)
	name, err := dns.NewName(labels)
	if err != nil {
		return err
	}

	var id uint16
	binary.Read(rand.Reader, binary.BigEndian, &id)
	query := &dns.Message{
		ID:    id,
		Flags: 0x0100,
		Question: []dns.Question{
			{
				Name:  name,
				Type:  c.qtype,
				Class: dns.ClassIN,
			},
		},

		Additional: []dns.RR{
			{
				Name:  dns.Name{},
				Type:  dns.RRTypeOPT,
				Class: c.edns,
				TTL:   0,
				Data:  []byte{},
			},
		},
	}
	buf, err := query.WireFormat()
	if err != nil {
		return err
	}

	_, err = transport.WriteTo(buf, addr)
	return err
}

func (c *DNSPacketConn) sendLoop(transport net.PacketConn, addr net.Addr) error {
	pollDelay := initPollDelay
	pollTimer := time.NewTimer(pollDelay)
	for {
		var p []byte
		outgoing := c.QueuePacketConn.OutgoingQueue(addr)
		pollTimerExpired := false

		select {
		case p = <-outgoing:
		default:
			select {
			case p = <-outgoing:
			case <-c.pollChan:
			case <-pollTimer.C:
				pollTimerExpired = true
			}
		}

		if len(p) > 0 {

			select {
			case <-c.pollChan:
			default:
			}
		}

		if pollTimerExpired {

			pollDelay = time.Duration(float64(pollDelay) * pollDelayMultiplier)
			if pollDelay > maxPollDelay {
				pollDelay = maxPollDelay
			}
		} else {

			if !pollTimer.Stop() {
				<-pollTimer.C
			}
			pollDelay = initPollDelay
		}
		pollTimer.Reset(pollDelay)

		err := c.send(transport, p, addr)
		if err != nil {
			log.Printf("send: %v", err)
			continue
		}
		c.stats.Queries.Add(1)
	}
}

type Stats struct {
	Queries   atomic.Int64
	Responses atomic.Int64
	Data      atomic.Int64
	Bad       atomic.Int64
	Rcode     atomic.Int64
	TC        atomic.Int64
	DNS64     atomic.Int64
	Bytes     atomic.Int64
	MaxRR     atomic.Int64
	MaxWire   atomic.Int64
	Cap       atomic.Int64
}

func (s *Stats) note(resp *dns.Message, wire int, broken bool, payload []byte) {
	s.Responses.Add(1)
	if resp.Flags&0x0200 != 0 {
		s.TC.Add(1)
	}
	if resp.Flags&0x000f != dns.RcodeNoError {
		s.Rcode.Add(1)
	}
	if n := int64(len(resp.Answer)); n > s.MaxRR.Load() {
		s.MaxRR.Store(n)
	}
	if int64(wire) > s.MaxWire.Load() {
		s.MaxWire.Store(int64(wire))
	}
	if broken {
		s.Bad.Add(1)
	}
	if len(payload) > 2 {
		s.Data.Add(1)
		s.Bytes.Add(int64(len(payload)))
	}
}

func (c *DNSPacketConn) Stats() *Stats { return &c.stats }

func synthesized(resp *dns.Message) bool {
	var datas [][]byte
	for _, a := range resp.Answer {
		if a.Type == RRTypeAAAA {
			datas = append(datas, a.Data)
		}
	}
	return IsDNS64(datas)
}

const (
	flightStart   = 64
	flightMin     = 64
	flightMax     = 64
	flightTimeout = 1600 * time.Millisecond
)

type flight struct {
	mu   sync.Mutex
	sent []time.Time
	cap  float64
	cut  time.Time
	wake chan struct{}
}

func newFlight() *flight {
	return &flight{cap: flightStart, wake: make(chan struct{}, 1)}
}

func (f *flight) limit() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int(f.cap)
}

func (f *flight) expire(now time.Time) {
	n := 0
	for n < len(f.sent) && now.Sub(f.sent[n]) > flightTimeout {
		n++
	}
	if n == 0 {
		return
	}
	f.sent = f.sent[n:]
	if now.Sub(f.cut) > flightTimeout {
		f.cut = now
		f.cap *= 0.85
		if f.cap < flightMin {
			f.cap = flightMin
		}
	}
}

func (f *flight) answered() {
	f.mu.Lock()
	if len(f.sent) > 0 {
		f.sent = f.sent[1:]
	}
	f.cap += 1 / f.cap
	if f.cap > flightMax {
		f.cap = flightMax
	}
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *flight) wait() {
	for {
		now := time.Now()
		f.mu.Lock()
		f.expire(now)
		if len(f.sent) < int(f.cap) {
			f.sent = append(f.sent, now)
			f.mu.Unlock()
			return
		}
		d := flightTimeout - now.Sub(f.sent[0]) + time.Millisecond
		f.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-f.wake:
		case <-t.C:
		}
		t.Stop()
	}
}
