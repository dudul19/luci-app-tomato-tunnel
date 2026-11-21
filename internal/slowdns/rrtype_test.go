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
