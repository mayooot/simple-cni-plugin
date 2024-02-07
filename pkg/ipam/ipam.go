package ipam

import (
	"errors"
	"fmt"
	"net"

	cip "github.com/containernetworking/plugins/pkg/ip"

	"github.com/mayooot/simple-cni-plugin/pkg/config"
	"github.com/mayooot/simple-cni-plugin/pkg/store"
)

var (
	IPOverflowError = errors.New("ip overflow")
)

type IPAM struct {
	subnet  *net.IPNet
	gateway net.IP
	store   *store.Store
}

func NewIPAM(conf *config.CNIConf, s *store.Store) (*IPAM, error) {
	// subnet: 10.244.0.0/12 via net.ParseCIDR
	// ipNet: {IP: 10.240.0.0, Mask: fff00000}
	// gateway: 10.240.0.1
	_, ipNet, err := net.ParseCIDR(conf.Subnet)
	if err != nil {
		return nil, err
	}
	ipam := &IPAM{
		subnet: ipNet,
		store:  s,
	}
	ipam.gateway, err = ipam.NextIP(ipam.subnet.IP)
	if err != nil {
		return nil, err
	}

	return ipam, nil
}

func (im *IPAM) Mask() net.IPMask {
	return im.subnet.Mask
}

func (im *IPAM) Gateway() net.IP {
	return im.gateway
}

func (im *IPAM) IPNet(ip net.IP) *net.IPNet {
	return &net.IPNet{
		IP:   ip,
		Mask: im.Mask(),
	}
}

func (im *IPAM) NextIP(ip net.IP) (net.IP, error) {
	nextIP := cip.NextIP(ip)
	if !im.subnet.Contains(nextIP) {
		return nil, IPOverflowError
	}
	return nextIP, nil
}

func (im *IPAM) AllocateIP(id, ifName string) (net.IP, error) {
	im.store.Lock()
	defer im.store.Unlock()

	if err := im.store.LoadData(); err != nil {
		return nil, err
	}
	ip, _ := im.store.GetIPByID(id)
	if len(ip) > 0 {
		return ip, nil
	}

	// when initialized for the first time, last is empty, but the gateway is already set.
	// e.g. subnet is 10.244.1.0/24, gateway is 10.244.1.1
	last := im.store.Last()
	if len(last) == 0 {
		last = im.gateway
	}

	start := make(net.IP, len(last))
	copy(start, last)

	// last will not change in the following loop
	for {
		nextIP, err := im.NextIP(start)
		if err != nil {
			// if the ip overflows, e.g. subnet is 10.244.1.0/24, nextIP is 10.244.2.0,
			// will use the gateway as a start
			if errors.Is(err, IPOverflowError) && !last.Equal(im.gateway) {
				start = im.gateway
				continue
			}
			return nil, err
		}

		if !im.store.Contain(nextIP) {
			err := im.store.Add(nextIP, id, ifName)
			return nextIP, err
		}

		start = nextIP
		// if no ip is available, the loop will break and an error will be returned
		// e.g. subnet is 10.244.1.0/24, last is 10.244.1.233
		// but 10.244.1.244 ~ 10.244.1.254 is already in use, so we can use gateway as a start
		// unfortunately, 10.244.1.2 ~ 10.244.1.232 is also already in use, so we can only break the loop
		if start.Equal(last) {
			break
		}

		fmt.Printf("ip: %s\n", nextIP)
	}

	return nil, fmt.Errorf("no avaiable ip")
}

func (im *IPAM) ReleaseIP(id string) error {
	im.store.Lock()
	defer im.store.Unlock()

	if err := im.store.LoadData(); err != nil {
		return err
	}
	return im.store.Del(id)
}

func (im *IPAM) CheckIP(id string) (net.IP, error) {
	im.store.Lock()
	defer im.store.Unlock()

	if err := im.store.LoadData(); err != nil {
		return nil, err
	}

	ip, ok := im.store.GetIPByID(id)
	if !ok {
		return nil, fmt.Errorf("failed to find container %s ip", id)
	}
	return ip, nil
}
