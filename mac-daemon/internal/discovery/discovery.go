package discovery

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"time"
)

const (
	DiscoveryPort = 42425
)

type DiscoveryPacket struct {
	Type       string `json:"type"`        // "DISCOVER_BEACON", "DISCOVER_REQUEST", "DISCOVER_RESPONSE"
	ServerName string `json:"server_name"` // e.g. "Ferit's Mac"
	WSPort     int    `json:"ws_port"`     // e.g. 42424
}

// getBroadcastAddresses returns broadcast addresses for all active IPv4 interfaces.
func getBroadcastAddresses() []net.IP {
	var broadcasts []net.IP
	broadcasts = append(broadcasts, net.IPv4(255, 255, 255, 255))

	ifaces, err := net.Interfaces()
	if err != nil {
		return broadcasts
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ip := ipNet.IP.To4()
			mask := ipNet.Mask
			if len(mask) == 4 {
				broadcast := net.IP(make([]byte, 4))
				for i := 0; i < 4; i++ {
					broadcast[i] = ip[i] | ^mask[i]
				}
				broadcasts = append(broadcasts, broadcast)
			}
		}
	}
	return broadcasts
}

// StartBroadcaster starts both a UDP broadcast beacon and a listener on port 42425.
func StartBroadcaster(ctx context.Context, serverName string, wsPort int) {
	// 1. Start listener for incoming discovery requests
	go func() {
		addr := net.UDPAddr{
			Port: DiscoveryPort,
			IP:   net.IPv4zero,
		}
		conn, err := net.ListenUDP("udp4", &addr)
		if err != nil {
			log.Printf("[Discovery] UDP Listen error on port %d: %v", DiscoveryPort, err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				n, remoteAddr, err := conn.ReadFromUDP(buf)
				if err != nil {
					continue
				}

				var req DiscoveryPacket
				if err := json.Unmarshal(buf[:n], &req); err == nil {
					if req.Type == "DISCOVER_REQUEST" {
						// Respond directly to the sender
						resp := DiscoveryPacket{
							Type:       "DISCOVER_RESPONSE",
							ServerName: serverName,
							WSPort:     wsPort,
						}
						respBytes, _ := json.Marshal(resp)
						_, _ = conn.WriteToUDP(respBytes, remoteAddr)

						// Also send to sender on DiscoveryPort in case they listened there
						targetAddr := &net.UDPAddr{
							IP:   remoteAddr.IP,
							Port: DiscoveryPort,
						}
						_, _ = conn.WriteToUDP(respBytes, targetAddr)

						log.Printf("[Discovery] Answered discovery request from %v", remoteAddr)
					}
				}
			}
		}
	}()

	// 2. Periodic broadcast beacon so mobile devices can detect Mac automatically
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		beacon := DiscoveryPacket{
			Type:       "DISCOVER_BEACON",
			ServerName: serverName,
			WSPort:     wsPort,
		}
		beaconData, _ := json.Marshal(beacon)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				targets := getBroadcastAddresses()
				for _, ip := range targets {
					addr := &net.UDPAddr{IP: ip, Port: DiscoveryPort}
					conn, err := net.DialUDP("udp4", nil, addr)
					if err == nil {
						_, _ = conn.Write(beaconData)
						_ = conn.Close()
					}
				}
			}
		}
	}()
}
