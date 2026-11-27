package slowdns

import (
	"bytes"
	"testing"

	"www.bamsoftware.com/git/dnstt.git/dns"
)

func encName(p []byte, domain dns.Name) []byte {
	enc := bytes.ToLower([]byte(nameBase32.EncodeToString(p)))
	var out []byte
	for len(enc) > 0 {
		n := len(enc)
		if n > 63 {
			n = 63
		}
		out = append(out, byte(n))
		out = append(out, enc[:n]...)
		enc = enc[n:]
	}
	for _, l := range domain {
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return append(out, 0)
}

func TestDecodeRData(t *testing.T) {
	domain, _ := dns.ParseName("ns.example.com")
	payload := bytes.Repeat([]byte{0x00, 0x7f, 0xff, 0x10}, 30)
	name := encName(payload, domain)
	cases := map[uint16][]byte{
		RRTypeNULL:  payload,
		RRTypeCNAME: name,
		RRTypeNS:    name,
		RRTypeMX:    append([]byte{0, 0}, name...),
		RRTypeSRV:   append([]byte{0, 0, 0, 0, 0, 0}, name...),
		RRTypeTXT:   append([]byte{byte(len(payload))}, payload...),
	}
	for typ, rdata := range cases {
		got, err := decodeRData(typ, rdata, domain)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s: %v %x", TypeName(typ), err, got)
		}
	}
	short := encName(payload, nil)
	short = append(short[:len(short)-1], 0xc0, 0x0c)
	got, err := decodeRData(RRTypeCNAME, short, domain)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("compressed: %v", err)
	}
}

func TestDecodeWrappedTypes(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x18, 0x0d, 0x8f, 0xff}, 20)
	l := len(payload) + 1
	svcb := append([]byte{0x00, 0x01, 0x00, 0xff, 0x00, byte(l >> 8), byte(l), 0x01}, payload...)
	dnskey := append([]byte{0x00, 0x00, 0x03, 0xfd, 6, 'd', 'n', 's', 'k', 'e', 'y', 7, 'e', 'p', 'r', 'o', 'd', 'e', 'v', 3, 'o', 'r', 'g', 0}, payload...)
	for typ, rdata := range map[uint16][]byte{RRTypeHTTPS: svcb, RRTypeSVCB: svcb, RRTypeDNSKEY: dnskey} {
		got, err := decodeRData(typ, rdata, nil)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s: %v %x", TypeName(typ), err, got)
		}
	}

	got, err := decodeRData(RRTypeSVCB, []byte{0x00, 0x01, 0x00, 0xff, 0x00, 0x00, 0x01, 0x01}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty svcb: %v %x", err, got)
	}
}

func TestDecodeNameDnsttInvalid(t *testing.T) {
	domain, _ := dns.ParseName("ns.example.com")
	payload := []byte("hello tomato 4.1")
	suffix, _ := dns.ParseName("dnstt.invalid")
	got, err := decodeRData(RRTypeCNAME, encName(payload, suffix), domain)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("dnstt.invalid suffix: %v %q", err, got)
	}
}

func serverAddrRData(t uint16, payload []byte) [][]byte {
	chunk, width := 3, 4
	if t == RRTypeAAAA {
		chunk, width = 15, 16
	}
	framed := append([]byte{byte(len(payload) >> 8), byte(len(payload))}, payload...)
	n := (len(framed) + chunk - 1) / chunk
	var out [][]byte
	for i := 0; i < n; i++ {
		d := make([]byte, width)
		d[0] = byte(i)
		end := i*chunk + chunk
		if end > len(framed) {
			end = len(framed)
		}
		copy(d[1:], framed[i*chunk:end])
		out = append(out, d)
	}
	return out
}

func TestDecodeAddrAnswers(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x18, 0x0d, 0x8f, 0xff, 0x52, 0x01}, 21)
	for _, typ := range []uint16{RRTypeA, RRTypeAAAA} {
		rr := serverAddrRData(typ, payload)
		got, err := decodeAddrAnswers(typ, rr)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("type %d in order: err=%v equal=%v", typ, err, bytes.Equal(got, payload))
		}
		for i, j := 0, len(rr)-1; i < j; i, j = i+1, j-1 {
			rr[i], rr[j] = rr[j], rr[i]
		}
		got, err = decodeAddrAnswers(typ, rr)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("type %d reversed: err=%v", typ, err)
		}
		if _, err := decodeAddrAnswers(typ, rr[1:]); err == nil {
			t.Fatalf("type %d: missing chunk must fail", typ)
		}
	}
	if _, err := decodeAddrAnswers(RRTypeA, [][]byte{{0, 0, 9, 1}}); err == nil {
		t.Fatal("length beyond data must fail")
	}
	if got, err := decodeAddrAnswers(RRTypeA, [][]byte{{0, 0, 0, 0}}); err != nil || len(got) != 0 {
		t.Fatal("empty payload must decode to nothing")
	}
}

func TestDecodeDNS64(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x18, 0x0d, 0x8f, 0xff, 0x52, 0x01}, 5)
	synth := func(pre []byte, a [][]byte) [][]byte {
		var out [][]byte
		for _, d := range a {
			r := make([]byte, 16)
			copy(r, pre)
			copy(r[12:], d)
			out = append(out, r)
		}
		return out
	}
	wkp := []byte{0x00, 0x64, 0xff, 0x9b}
	nsp := []byte{0x24, 0x00, 0xcb, 0x00, 0x00, 0x64, 0, 0, 0, 0, 0, 0}
	a := serverAddrRData(RRTypeA, payload)
	for name, pre := range map[string][]byte{"wkp": wkp, "nsp": nsp} {
		got, err := decodeAddrAnswers(RRTypeAAAA, synth(pre, a))
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s: err=%v equal=%v", name, err, bytes.Equal(got, payload))
		}
	}

	one := serverAddrRData(RRTypeA, []byte{0x07})
	if got, err := decodeAddrAnswers(RRTypeAAAA, synth(wkp, one)); err != nil || !bytes.Equal(got, []byte{0x07}) {
		t.Fatalf("wkp single: %v %x", err, got)
	}

	for _, n := range []int{1, 7, 200} {
		rr := serverAddrRData(RRTypeAAAA, bytes.Repeat([]byte{0}, n))
		if IsDNS64(rr) {
			t.Fatalf("real AAAA (%d B) taken for DNS64", n)
		}
		if got, err := decodeAddrAnswers(RRTypeAAAA, rr); err != nil || len(got) != n {
			t.Fatalf("real AAAA (%d B): %v", n, err)
		}
	}
}
