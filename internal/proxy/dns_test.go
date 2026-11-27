package proxy

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

type fakeDNS struct {
	dials atomic.Int32
	limit int
}

func (f *fakeDNS) UDPGWPort() int { return 0 }

func (f *fakeDNS) Dial(string) (net.Conn, error) {
	f.dials.Add(1)
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		var wmu sync.Mutex
		var wg sync.WaitGroup
		h := make([]byte, 2)
		for n := 0; f.limit == 0 || n < f.limit; n++ {
			if _, err := io.ReadFull(b, h); err != nil {
				break
			}
			q := make([]byte, binary.BigEndian.Uint16(h))
			if _, err := io.ReadFull(b, q); err != nil {
				break
			}
			wg.Add(1)
			go func(q []byte) {
				defer wg.Done()
				time.Sleep(time.Duration(q[12]%5) * 5 * time.Millisecond)
				wmu.Lock()
				b.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(q))), q...))
				wmu.Unlock()
			}(q)
		}
		wg.Wait()
	}()
	return a, nil
}

func query(id uint16, tag byte) []byte {
	q := make([]byte, 13)
	binary.BigEndian.PutUint16(q, id)
	q[12] = tag
	return q
}

func TestDNSKeptConnections(t *testing.T) {
	up := &fakeDNS{}
	d := &DNS{Server: "1.1.1.1:53", Up: up, Log: logx.New("", 10, false)}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := query(uint16(1000+i), byte(i))
			ans := d.ask(q)
			if binary.BigEndian.Uint16(ans) != uint16(1000+i) || ans[12] != byte(i) || ans[2]&0x80 != 0 {
				t.Errorf("query %d: wrong answer % x", i, ans)
			}
		}(i)
	}
	wg.Wait()
	if n := up.dials.Load(); n > dnsConns+1 {
		t.Fatalf("%d dials for 200 queries, want about %d", n, dnsConns)
	}
}

func TestDNSRedialsAfterClose(t *testing.T) {
	up := &fakeDNS{limit: 3}
	d := &DNS{Server: "1.1.1.1:53", Up: up, Log: logx.New("", 10, false)}
	for i := 0; i < 20; i++ {
		ans := d.ask(query(uint16(i), 0))
		if binary.BigEndian.Uint16(ans) != uint16(i) || ans[2]&0x80 != 0 {
			t.Fatalf("query %d after server close: % x", i, ans)
		}
	}
}
