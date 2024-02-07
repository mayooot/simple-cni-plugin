package bridge

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

func CreateBridge(bridge string, mtu int, gateway *net.IPNet) (netlink.Link, error) {
	if l, _ := netlink.LinkByName(bridge); l != nil {
		return l, nil
	}

	br := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name:   bridge,
			MTU:    mtu,
			TxQLen: -1,
		},
	}
	if err := netlink.LinkAdd(br); err != nil && !errors.Is(err, syscall.EEXIST) {
		return nil, err
	}
	dev, err := netlink.LinkByName(bridge)
	if err != nil {
		return nil, err
	}
	if err = netlink.AddrAdd(dev, &netlink.Addr{IPNet: gateway}); err != nil {
		return nil, err
	}
	if err = netlink.LinkSetUp(dev); err != nil {
		return nil, err
	}
	return dev, nil
}

func SetupVeth(netNS ns.NetNS, br netlink.Link, mtu int, ifName string, podIP *net.IPNet, gateway net.IP) error {
	hostIface := &current.Interface{}
	err := netNS.Do(func(hostNS ns.NetNS) error {
		// create both veth devices and move the host-side veth into the provided hostNS namespace
		hostVeth, containerVeth, err := ip.SetupVeth(ifName, mtu, "", hostNS)
		if err != nil {
			return err
		}
		hostIface.Name = hostVeth.Name
		device, err := netlink.LinkByName(containerVeth.Name)
		if err != nil {
			return err
		}
		// set ip for container veth
		if err = netlink.AddrAdd(device, &netlink.Addr{IPNet: podIP}); err != nil {
			return err
		}
		// set up the container veth
		if err = netlink.LinkSetUp(device); err != nil {
			return err
		}
		// add default route, when you run `route -n`, you can see that:
		// Destination     Gateway         Genmask         Flags Metric Ref    Use Iface
		// 0.0.0.0         gateway         0.0.0.0         UG    0      0      0   eth0
		// the eth0 is actually container veth
		if err = ip.AddDefaultRoute(gateway, device); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostIface.Name)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", hostIface.Name, err)
	}

	if hostVeth == nil {
		return fmt.Errorf("nil hostveth")
	}

	if err = netlink.LinkSetMaster(hostVeth, br); err != nil {
		return fmt.Errorf("failed to connect %q to bridge %v: %v", hostVeth.Attrs().Name, br.Attrs().Name, err)
	}
	return nil
}

func DelVeth(netNS ns.NetNS, ifName string) error {
	return netNS.Do(func(ns.NetNS) error {
		device, err := netlink.LinkByName(ifName)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return netlink.LinkDel(device)
	})
}

func CheckVeth(netNS ns.NetNS, ifName string, ip net.IP) error {
	return netNS.Do(func(ns.NetNS) error {
		device, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}

		// get IPv4 address only
		ips, err := netlink.AddrList(device, netlink.FAMILY_V4)
		if err != nil {
			return err
		}
		for _, addr := range ips {
			if addr.IP.Equal(ip) {
				return nil
			}
		}
		return fmt.Errorf("failed to find ip %s for %s", ip, ifName)
	})
}
