package client

import "github.com/apernet/hysteria/core/v2/internal/protocol"

// TOMATO PATCH: UseZivpnHeaders membuat klien memakai host/header "Zivpnudp-*"
// seperti aplikasi ZiVPN (dipakai tes zivpn-server).
func UseZivpnHeaders(on bool) {
	if on {
		protocol.ClientFamily = protocol.FamilyZivpn
	} else {
		protocol.ClientFamily = protocol.FamilyHysteria
	}
}
