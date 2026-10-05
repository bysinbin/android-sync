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
	Type          string `json:"type"`                     // "DISCOVER_BEACON", "DISCOVER_REQUEST", "DISCOVER_RESPONSE"
	ServerName    string `json:"server_name"`              // e.g. "Ferit's Mac"
	WSPort        int    `json:"ws_port"`                  // e.g. 42424
	DeviceType    string `json:"device_type,omitempty"`    // "mac"
	PhoneName     string `json:"phone_name,omitempty"`     // e.g. "pond"
	PhoneModel    string `json:"phone_model,omitempty"`    // e.g. "2409BRN2CA"
	PhoneIP       string `json:"phone_ip,omitempty"`       // e.g. "192.168.50.118"
	PhoneBattery  int    `json:"phone_battery,omitempty"`  // e.g. 53
	PhoneCharging bool   `json:"phone_charging,omitempty"`
	PairingPIN    string `json:"pairing_pin,omitempty"`
	IsPaired      bool   `json:"is_paired,omitempty"`
}

type ExtraInfoProvider func() (phoneName, phoneModel, phoneIP string, phoneBattery int, phoneCharging bool, pin string, isPaired bool)

func createPacket(packetType, serverName string, wsPort int, provider ExtraInfoProvider) DiscoveryPacket {
	p := DiscoveryPacket{
		Type:       packetType,
		ServerName: serverName,
		WSPort:     wsPort,
		DeviceType: "mac",
	}
	if provider != nil {
		name, model, ip, battery, charging, pin, isPaired := provider()
		p.PhoneName = name
		p.PhoneModel = model
		p.PhoneIP = ip
		p.PhoneBattery = battery
		p.PhoneCharging = charging
		p.PairingPIN = pin
		p.IsPaired = isPaired
	}
	return p
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
	StartBroadcasterWithProvider(ctx, serverName, wsPort, nil)
}

func StartBroadcasterWithProvider(ctx context.Context, serverName string, wsPort int, provider ExtraInfoProvider) {
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
						resp := createPacket("DISCOVER_RESPONSE", serverName, wsPort, provider)
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

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				beacon := createPacket("DISCOVER_BEACON", serverName, wsPort, provider)
				beaconData, _ := json.Marshal(beacon)
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
