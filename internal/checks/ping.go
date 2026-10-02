package checks

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/audemed44/lookout/internal/store"
)

// ICMP echo over an unprivileged "ping socket" (SOCK_DGRAM, IPPROTO_ICMP),
// so Lookout needs no root or CAP_NET_RAW. Docker allows these sockets for
// every group by default (net.ipv4.ping_group_range); on a host that
// doesn't, the check says so.

var pingSeq atomic.Uint32

func probePing(ctx context.Context, c store.Check) Outcome {
	start := time.Now()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, c.Target)
	if err != nil {
		return fail(start, "%s", describeHTTPError(err))
	}
	if len(ips) == 0 {
		return fail(start, "no address for %s", c.Target)
	}
	ip := ips[0].IP
	for _, a := range ips {
		if a.IP.To4() != nil {
			ip = a.IP
			break
		}
	}
	rtt, err := echo(ctx, ip)
	if err != nil {
		return fail(start, "%v", err)
	}
	return Outcome{OK: true, Latency: rtt, Message: "reply from " + ip.String()}
}

func echo(ctx context.Context, ip net.IP) (time.Duration, error) {
	v4 := ip.To4() != nil
	family, proto, echoType, replyType := syscall.AF_INET, syscall.IPPROTO_ICMP, byte(8), byte(0)
	if !v4 {
		family, proto, echoType, replyType = syscall.AF_INET6, syscall.IPPROTO_ICMPV6, 128, 129
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, proto)
	if err != nil {
		return 0, fmt.Errorf("can't open a ping socket (%v): allow it with sysctl net.ipv4.ping_group_range", err)
	}
	f := os.NewFile(uintptr(fd), "icmp")
	conn, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	seq := uint16(pingSeq.Add(1))
	msg := make([]byte, 16)
	msg[0] = echoType
	binary.BigEndian.PutUint16(msg[6:], seq) // the kernel fills in the ID
	binary.BigEndian.PutUint64(msg[8:], uint64(time.Now().UnixNano()))
	if v4 {
		binary.BigEndian.PutUint16(msg[2:], checksum(msg))
	}
	var dst net.Addr = &net.UDPAddr{IP: ip}
	start := time.Now()
	if _, err := conn.WriteTo(msg, dst); err != nil {
		return 0, fmt.Errorf("send: %v", err)
	}
	buf := make([]byte, 512)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return 0, fmt.Errorf("no reply from %s", ip)
			}
			return 0, err
		}
		if n >= 8 && buf[0] == replyType && binary.BigEndian.Uint16(buf[6:]) == seq {
			return time.Since(start), nil
		}
	}
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
