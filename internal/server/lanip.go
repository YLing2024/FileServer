package server

import "net"

// LanIPs 返回本机所有非回环、非 APIPA 的 IPv4 地址
func LanIPs() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			// 跳过链路本地地址
			if ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			out = append(out, ip4.String())
		}
	}
	return out
}
