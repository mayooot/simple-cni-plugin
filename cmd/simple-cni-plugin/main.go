package main

import (
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/buildversion"

	"github.com/mayooot/simple-cni-plugin/pkg/bridge"
	"github.com/mayooot/simple-cni-plugin/pkg/config"
	"github.com/mayooot/simple-cni-plugin/pkg/ipam"
	"github.com/mayooot/simple-cni-plugin/pkg/store"
)

const (
	pluginName = "simple-cni-plugin"
)

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, buildversion.BuildString(pluginName))
}

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := config.LoadCNIConfig(args.StdinData)
	if err != nil {
		return err
	}
	s, err := store.NewStore(conf.DataDir, conf.Name)
	if err != nil {
		return err
	}
	defer s.Close()

	im, err := ipam.NewIPAM(conf, s)
	if err != nil {
		return fmt.Errorf("failed to create ipam: %v", err)
	}
	gateway := im.Gateway()
	ip, err := im.AllocateIP(args.ContainerID, args.IfName)
	if err != nil {
		return err
	}

	mtu := 1500
	br, err := bridge.CreateBridge(conf.Bridge, mtu, im.IPNet(gateway))
	if err != nil {
		return err
	}

	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netNS.Close()

	if err = bridge.SetupVeth(netNS, br, mtu, args.IfName, im.IPNet(ip), gateway); err != nil {
		return err
	}

	result := &current.Result{
		CNIVersion: current.ImplementedSpecVersion,
		IPs: []*current.IPConfig{
			{
				Address: *im.IPNet(ip),
				Gateway: gateway,
			},
		},
	}

	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := config.LoadCNIConfig(args.StdinData)
	if err != nil {
		return err
	}
	s, err := store.NewStore(conf.DataDir, conf.Name)
	if err != nil {
		return err
	}
	defer s.Close()

	im, err := ipam.NewIPAM(conf, s)
	if err != nil {
		return fmt.Errorf("failed to create ipam: %v", err)
	}
	if err = im.ReleaseIP(args.ContainerID); err != nil {
		return err
	}

	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netNS.Close()

	return bridge.DelVeth(netNS, args.IfName)
}

func cmdCheck(args *skel.CmdArgs) error {
	conf, err := config.LoadCNIConfig(args.StdinData)
	if err != nil {
		return err
	}
	s, err := store.NewStore(conf.DataDir, conf.Name)
	if err != nil {
		return err
	}
	defer s.Close()

	im, err := ipam.NewIPAM(conf, s)
	if err != nil {
		return fmt.Errorf("failed to create ipam: %v", err)
	}
	ip, err := im.CheckIP(args.ContainerID)
	if err != nil {
		return err
	}
	netNS, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netNS.Close()

	return bridge.CheckVeth(netNS, args.IfName, ip)
}
