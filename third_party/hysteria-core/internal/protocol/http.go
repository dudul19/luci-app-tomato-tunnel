package protocol

// TOMATO PATCH (zivpn-server): klien ZiVPN memakai header "Zivpnudp-*" dan
// host "zivpnudp" sebagai pengganti "Hysteria-*"/"hysteria". Server menerima
// kedua keluarga dan membalas dengan keluarga yang sama dengan request.
// Lihat PATCHES.md.

import (
	"net/http"
	"strconv"
)

const (
	URLHost = "hysteria"
	URLPath = "/auth"

	RequestHeaderAuth        = "Hysteria-Auth"
	ResponseHeaderUDPEnabled = "Hysteria-UDP"
	CommonHeaderCCRX         = "Hysteria-CC-RX"
	CommonHeaderPadding      = "Hysteria-Padding"

	StatusAuthOK = 233
)

// Family — satu set nama host/header protokol.
type Family struct {
	Host, Auth, UDP, CCRX, Padding string
}

var (
	FamilyHysteria = Family{URLHost, RequestHeaderAuth, ResponseHeaderUDPEnabled, CommonHeaderCCRX, CommonHeaderPadding}
	FamilyZivpn    = Family{"zivpnudp", "Zivpnudp-Auth", "Zivpnudp-UDP", "Zivpnudp-CC-RX", "Zivpnudp-Padding"}

	// ClientFamily — keluarga yang dipakai package client (default Hysteria;
	// tes zivpn-server menggantinya ke FamilyZivpn).
	ClientFamily = FamilyHysteria
)

// FamilyByHost mengembalikan keluarga untuk host request auth, ok=false bila
// host bukan host auth yang dikenal.
func FamilyByHost(host string) (Family, bool) {
	switch host {
	case FamilyZivpn.Host:
		return FamilyZivpn, true
	case FamilyHysteria.Host:
		return FamilyHysteria, true
	}
	return Family{}, false
}

// FamilyOf menebak keluarga dari header request (header Zivpnudp-Auth menang).
func FamilyOf(h http.Header, byHost Family) Family {
	if h.Get(FamilyZivpn.Auth) != "" {
		return FamilyZivpn
	}
	if h.Get(FamilyHysteria.Auth) != "" {
		return FamilyHysteria
	}
	return byHost
}

// AuthRequest is what client sends to server for authentication.
type AuthRequest struct {
	Auth string
	Rx   uint64 // 0 = unknown, client asks server to use bandwidth detection
}

// AuthResponse is what server sends to client when authentication is passed.
type AuthResponse struct {
	UDPEnabled bool
	Rx         uint64 // 0 = unlimited
	RxAuto     bool   // true = server asks client to use bandwidth detection
}

func AuthRequestFromHeader(h http.Header) AuthRequest {
	return AuthRequestFromHeaderF(h, FamilyHysteria)
}

func AuthRequestFromHeaderF(h http.Header, f Family) AuthRequest {
	rx, _ := strconv.ParseUint(h.Get(f.CCRX), 10, 64)
	return AuthRequest{
		Auth: h.Get(f.Auth),
		Rx:   rx,
	}
}

func AuthRequestToHeader(h http.Header, req AuthRequest) {
	f := ClientFamily
	h.Set(f.Auth, req.Auth)
	h.Set(f.CCRX, strconv.FormatUint(req.Rx, 10))
	h.Set(f.Padding, authRequestPadding.String())
}

func AuthResponseFromHeader(h http.Header) AuthResponse {
	f := ClientFamily
	resp := AuthResponse{}
	resp.UDPEnabled, _ = strconv.ParseBool(h.Get(f.UDP))
	rxStr := h.Get(f.CCRX)
	if rxStr == "auto" {
		// Special case for server requesting client to use bandwidth detection
		resp.RxAuto = true
	} else {
		resp.Rx, _ = strconv.ParseUint(rxStr, 10, 64)
	}
	return resp
}

func AuthResponseToHeader(h http.Header, resp AuthResponse) {
	AuthResponseToHeaderF(h, resp, FamilyHysteria)
}

func AuthResponseToHeaderF(h http.Header, resp AuthResponse, f Family) {
	h.Set(f.UDP, strconv.FormatBool(resp.UDPEnabled))
	if f.Host == FamilyZivpn.Host {
		// mt-udp juga mengirim header ini ke klien ZiVPN
		h.Set("X-UDP-Enabled", strconv.FormatBool(resp.UDPEnabled))
	}
	if resp.RxAuto {
		h.Set(f.CCRX, "auto")
	} else {
		h.Set(f.CCRX, strconv.FormatUint(resp.Rx, 10))
	}
	h.Set(f.Padding, authResponsePadding.String())
}
