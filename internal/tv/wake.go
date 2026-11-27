package tv

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"syscall"
	"time"
)

func validMACs(addresses []string) []string {
	var result []string
	for _, address := range addresses {
		compact := strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(address))
		decoded, err := hex.DecodeString(compact)
		if err == nil && len(decoded) == 6 {
			mac := net.HardwareAddr(decoded).String()
			if !slices.Contains(result, mac) {
				result = append(result, mac)
			}
		}
	}
	return result
}

func (c Controller) PowerOn(ctx context.Context) error {
	cfg, err := c.store.load()
	if err != nil {
		return err
	}
	macs := validMACs(append([]string{cfg.MAC}, cfg.WakeMACs...))
	if len(macs) == 0 {
		return errors.New("missing TV MAC address for Wake-on-LAN")
	}
	dialer := net.Dialer{
		Timeout: 2 * time.Second,
		Control: func(_, _ string, conn syscall.RawConn) error {
			var sockErr error
			err := conn.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			})
			return errors.Join(err, sockErr)
		},
	}
	targets := wakeTargets(ctx, cfg.Host)
	var sent bool
	var lastError error
	for _, target := range targets {
		for _, port := range []string{"9", "7"} {
			conn, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(target, port))
			if err != nil {
				lastError = err
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			for _, address := range macs {
				mac, _ := net.ParseMAC(address)
				packet := append(bytes.Repeat([]byte{255}, 6), bytes.Repeat(mac, 16)...)
				if _, err := conn.Write(packet); err != nil {
					lastError = err
				} else {
					sent = true
				}
			}
			conn.Close()
		}
	}
	if !sent && lastError != nil {
		return fmt.Errorf("Wake-on-LAN: %w", lastError)
	}
	return nil
}

func wakeTargets(ctx context.Context, host string) []string {
	targets := []string{"255.255.255.255"}
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tvAddresses, _ := net.DefaultResolver.LookupNetIP(resolveCtx, "ip4", host)
	localAddresses, _ := net.InterfaceAddrs()
	for _, local := range localAddresses {
		network, ok := local.(*net.IPNet)
		if !ok || network.IP.To4() == nil {
			continue
		}
		ones, bits := network.Mask.Size()
		if bits != 32 || ones >= 31 {
			continue
		}
		prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte(network.IP.To4())), ones)
		for _, address := range tvAddresses {
			if prefix.Contains(address.Unmap()) {
				broadcast := [4]byte(network.IP.To4())
				for i := range broadcast {
					broadcast[i] |= ^network.Mask[i]
				}
				target := netip.AddrFrom4(broadcast).String()
				if !slices.Contains(targets, target) {
					targets = append(targets, target)
				}
				break
			}
		}
	}
	return targets
}
