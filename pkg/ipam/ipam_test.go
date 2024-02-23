package ipam

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNextIP(t *testing.T) {
	subnet := "10.244.1.0/24"
	_, ipNet, _ := net.ParseCIDR(subnet)
	im := &IPAM{subnet: &net.IPNet{
		IP:   ipNet.IP,
		Mask: ipNet.Mask,
	}}

	lastIP := net.ParseIP("10.244.1.255")
	// if we call containernetworking/plugins.NextIP(), it will return 10.244.2.0
	ip, err := im.NextIP(lastIP)
	require.Nil(t, ip)
	require.Equal(t, err, IPOverflowError)
}
