// simple-cni-plugin-daemonset will be deployed on every node in K8s Cluster.
// The main function is to listen to all node resources in the cluster, and when a node's pod CIDR changes,
// trigger a reconcile, and change the route rules of the current host.
// When daemonset starts for the first time, it will save the pod CIDR and bridge name (default is cni0) as a subnet range
// in /run/simple-cni-plugin-subnet.json on the host, and create the bridge using the first available IP of the pod CIDR.
// If iptables feature is enabled, it will use iptables to create the corresponding rules.
// For example, it will allow packets to be forwarded through the bridge and the default network interface,
// and packets int pod CIDR range leaving the current host do NAT.

// Reconciler
// When reconcile is triggered, it processes all nodes except itself, performing the following steps.
// Get the pod CIDR of a node, get the IP of the node used for intra-cluster communication,
// and generate a routing rule: dst is pod CIDR, gateway is node IP.
// Add the generated rule if it doesn't exist on the current node, or compare it to update it if it already exists.
// Finally, delete the route rules of the removed nodes.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/mayooot/simple-cni-plugin/pkg/bridge"
	config2 "github.com/mayooot/simple-cni-plugin/pkg/config"
)

const (
	appName = "simple-cni-plugin-daemonSet"
)

var (
	log = logf.Log.WithName(appName)
)

type daemonConf struct {
	clusterCIDR    string
	nodeName       string
	enableIptables bool
}

func (c *daemonConf) addFlags() {
	flag.StringVar(&c.clusterCIDR, "cluster-cidr", "", "cluster pod cidr")
	flag.StringVar(&c.nodeName, "node", "", "current node name")
	flag.BoolVar(&c.enableIptables, "enable-iptables", false, "add iptables forward and nat rules")
}

func (c *daemonConf) parseConfig() error {
	if _, _, err := net.ParseCIDR(c.clusterCIDR); err != nil {
		return fmt.Errorf("cluster-cidr is invaild: %v", err)
	}
	if len(c.nodeName) == 0 {
		c.nodeName = os.Getenv("NODE_NAME")
	}
	if len(c.nodeName) == 0 {
		return fmt.Errorf("node name is empty")
	}
	return nil
}

func main() {
	logf.SetLogger(zap.New())

	conf := &daemonConf{}
	conf.addFlags()

	flag.Parse()

	if err := conf.parseConfig(); err != nil {
		log.Error(err, "failed to parse config")
		os.Exit(1)
	}

	if err := RunController(conf); err != nil {
		log.Error(err, "failed to run controller")
		os.Exit(1)
	}
}

func RunController(conf *daemonConf) error {
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{})
	if err != nil {
		log.Error(err, "failed to create manager")
		return err
	}

	reconciler, err := NewReconciler(conf, mgr)
	if err != nil {
		return err
	}
	log.Info("create manager success")

	err = builder.
		ControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			// if assert failed or node's podCIDR has changed, it should be processed
			UpdateFunc: func(event event.UpdateEvent) bool {
				oldNode, ok := event.ObjectOld.(*corev1.Node)
				if !ok {
					return true
				}
				newNode, ok := event.ObjectNew.(*corev1.Node)
				if !ok {
					return true
				}
				return oldNode.Spec.PodCIDR != newNode.Spec.PodCIDR
			},
		}).Complete(reconciler)
	if err != nil {
		log.Error(err, "failed to create controller")
		return err
	}

	return mgr.Start(signals.SetupSignalHandler())
}

type Reconciler struct {
	client      client.Client
	clusterCIDR *net.IPNet

	hostLink     netlink.Link
	routes       map[string]netlink.Route
	config       *daemonConf
	subnetConfig *config2.SubnetConf
}

func NewReconciler(conf *daemonConf, mgr manager.Manager) (*Reconciler, error) {
	_, clusterCIDR, err := net.ParseCIDR(conf.clusterCIDR)
	if err != nil {
		return nil, err
	}

	// get node info from K8s cluster, equivalent to: `kubectl describe node node-xxx`
	node := &corev1.Node{}
	if err = mgr.GetAPIReader().Get(context.TODO(), types.NamespacedName{Name: conf.nodeName}, node); err != nil {
		return nil, err
	}

	// hostIP can route only within the cluster
	hostIP, err := getNodeInternalIP(node)
	if err != nil {
		return nil, fmt.Errorf("failed to get host ip for node %s", conf.nodeName)

	}
	_, podCIDR, err := net.ParseCIDR(node.Spec.PodCIDR)
	if err != nil {
		return nil, err
	}

	log.Info("get node info", "host ip", hostIP.String(), "node clusterCIDR", podCIDR.String())

	subnetConf := &config2.SubnetConf{
		Subnet: podCIDR.String(),
		Bridge: config2.DefaultBridgeName,
	}
	if err := config2.StoreSubnetConfig(subnetConf); err != nil {
		return nil, err
	}

	var hostLink netlink.Link
	linkList, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
Loop:
	for _, link := range linkList {
		if link.Attrs() != nil {
			addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
			if err != nil {
				return nil, err
			}
			for _, addr := range addrs {
				if addr.IP.Equal(hostIP) {
					hostLink = link
					break Loop
				}
			}
		}
	}
	if hostLink == nil {
		return nil, fmt.Errorf("failed to get host link device")
	}
	log.Info(fmt.Sprintf("get host link success, type: %s, name: %s, index: %d", hostLink.Type(), hostLink.Attrs().Name, hostLink.Attrs().Index))

	if _, err = bridge.CreateBridge(subnetConf.Bridge, 1500, &net.IPNet{IP: ip.NextIP(podCIDR.IP), Mask: podCIDR.Mask}); err != nil {
		return nil, err
	}

	if conf.enableIptables {
		if err = addIptables(subnetConf.Bridge, hostLink.Attrs().Name, subnetConf.Subnet); err != nil {
			return nil, err
		}
		log.Info("set iptables success")
	}

	routes := make(map[string]netlink.Route)
	routeList, err := netlink.RouteList(hostLink, netlink.FAMILY_V4)
	for _, route := range routeList {
		if route.Dst != nil && !route.Dst.IP.Equal(podCIDR.IP) && clusterCIDR.Contains(route.Dst.IP) {
			routes[route.Dst.String()] = route
		}
	}
	log.Info("get local routes", "routes", routes)

	return &Reconciler{
		client:       mgr.GetClient(),
		clusterCIDR:  clusterCIDR,
		hostLink:     hostLink,
		routes:       routes,
		config:       conf,
		subnetConfig: subnetConf,
	}, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log.Info("start reconcile", "key", req.NamespacedName.Name)
	result := reconcile.Result{}
	nodes := &corev1.NodeList{}
	if err := r.client.List(ctx, nodes); err != nil {
		return result, err
	}

	routes := make(map[string]netlink.Route)
	for _, node := range nodes.Items {
		if node.Name == r.config.nodeName {
			continue
		}
		if len(node.Spec.PodCIDRs) == 0 {
			continue
		}
		_, podCIDR, err := net.ParseCIDR(node.Spec.PodCIDR)
		if err != nil {
			return result, err
		}
		nodeIP, err := getNodeInternalIP(&node)
		if err != nil {
			log.Error(err, "failed to get host")
			continue
		}
		route := netlink.Route{
			Dst:        podCIDR,
			Gw:         nodeIP,
			ILinkIndex: r.hostLink.Attrs().Index,
		}
		routes[podCIDR.String()] = route

		if currentRoute, ok := r.routes[podCIDR.String()]; ok {
			if isRouteEqual(route, currentRoute) {
				continue
			}
			if err := r.ReplaceRoute(currentRoute); err != nil {
				return result, err
			}
		} else {
			if err := r.addRoute(route); err != nil {
				return result, err
			}
		}
	}

	for podCIDR, route := range r.routes {
		if _, ok := routes[podCIDR]; !ok {
			if err := r.delRoute(route); err != nil {
				return result, err
			}
		}
	}

	return result, nil
}

func (r *Reconciler) addRoute(route netlink.Route) (err error) {
	defer func() {
		if err == nil {
			r.routes[route.Dst.String()] = route
		}
	}()

	log.Info(fmt.Sprintf("add route: %s", route.String()))
	err = netlink.RouteAdd(&route)
	if err != nil {
		log.Error(err, "failed to add route", "route", route.String())
	}
	return
}

func (r *Reconciler) delRoute(route netlink.Route) (err error) {
	defer func() {
		if err == nil {
			delete(r.routes, route.Dst.String())
		}
	}()

	log.Info(fmt.Sprintf("del route: %s", route.String()))
	err = netlink.RouteDel(&route)
	return
}

func (r *Reconciler) ReplaceRoute(route netlink.Route) (err error) {
	defer func() {
		if err == nil {
			r.routes[route.Dst.String()] = route
		}
	}()

	log.Info(fmt.Sprintf("replace route: %s", route.String()))
	err = netlink.RouteReplace(&route)
	return
}

func addIptables(bridgeName, hostDeviceName, podCIDR string) error {
	ipt, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		return err
	}

	if err = ipt.AppendUnique("filter", "FORWARD", "-i", bridgeName, "-j", "ACCEPT"); err != nil {
		return err
	}
	if err = ipt.AppendUnique("filter", "FORWARD", "-i", hostDeviceName, "-j", "ACCEPT"); err != nil {
		return err
	}
	if err = ipt.AppendUnique("nat", "POSTROUTING", "-s", podCIDR, "-j", "MASQUERADE"); err != nil {
		return err
	}
	return nil
}

// NodeInternalIP is the IP address that a node can route only within the cluster
func getNodeInternalIP(node *corev1.Node) (net.IP, error) {
	if node == nil {
		return nil, fmt.Errorf("empty node")
	}
	var ip net.IP
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			ip = net.ParseIP(addr.Address)
			break
		}
	}
	if len(ip) == 0 {
		return nil, fmt.Errorf("node %s ip is nil", node.Name)
	}
	return ip, nil
}

func isRouteEqual(r1, r2 netlink.Route) bool {
	return r1.Dst.IP.Equal(r2.Dst.IP) && r1.Gw.Equal(r2.Gw) && bytes.Equal(r1.Dst.Mask, r2.Dst.Mask) && r1.LinkIndex == r2.LinkIndex
}
