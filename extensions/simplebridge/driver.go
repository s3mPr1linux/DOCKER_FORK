package simplebridge

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/network"
	"github.com/docker/docker/pkg/iptables"
	"github.com/docker/docker/sandbox"
	"github.com/docker/docker/state"

	"github.com/vishvananda/netlink"
)

const maxVethName = 8

type BridgeDriver struct {
	state state.State
	mutex sync.Mutex
}

func (d *BridgeDriver) GetNetwork(id string) (network.Network, error) {
	return d.loadNetwork(id)
}

func (d *BridgeDriver) Restore(s state.State) error {
	d.state = s
	return nil
}

func (d *BridgeDriver) loadEndpoint(name, endpoint string) (*BridgeEndpoint, error) {
	iface, err := d.getEndpointProperty(name, endpoint, "interfaceName")
	if err != nil {
		return nil, err
	}

	hwAddr, err := d.getEndpointProperty(name, endpoint, "hwAddr")
	if err != nil {
		return nil, err
	}

	mtu, err := d.getEndpointProperty(name, endpoint, "mtu")
	if err != nil {
		return nil, err
	}

	mtuInt, _ := strconv.ParseUint(mtu, 10, 32)

	network, err := d.loadNetwork(name)
	if err != nil {
		return nil, err
	}

	return &BridgeEndpoint{
		ID:            endpoint,
		interfaceName: iface,
		hwAddr:        hwAddr,
		mtu:           uint(mtuInt),
		network:       network,
	}, nil
}

func (d *BridgeDriver) saveEndpoint(name string, ep *BridgeEndpoint) error {
	if err := d.setEndpointProperty(name, ep.ID, "interfaceName", ep.ID); err != nil {
		return err
	}

	if err := d.setEndpointProperty(name, ep.ID, "hwAddr", ep.hwAddr); err != nil {
		return err
	}

	if err := d.setEndpointProperty(name, ep.ID, "mtu", strconv.Itoa(int(ep.mtu))); err != nil {
		return err
	}

	return nil
}

func vethNameTooLong(name string) bool {
	return len(name) > maxVethName // FIXME write a test for this
}

// discovery driver? should it be hooked here or in the core?
func (d *BridgeDriver) Link(id, name string, s sandbox.Sandbox, replace bool) (network.Endpoint, error) {
	if vethNameTooLong(name) {
		return nil, fmt.Errorf("name %q is too long, must be 8 characters", name)
	}

	d.mutex.Lock()
	defer d.mutex.Unlock()

	network, err := d.loadNetwork(id)
	if err != nil {
		return nil, err
	}

	ep := &BridgeEndpoint{
		network: network,
		ID:      name,
	}

	if ep, err := d.loadEndpoint(id, name); ep != nil && err != nil && !replace {
		return nil, fmt.Errorf("Endpoint %q already taken", name)
	}

	if err := d.createEndpoint(id, name); err != nil {
		fmt.Println("[fail] d.createEndpoint")
		return nil, err
	}

	if err := ep.configure(name, s); err != nil {
		fmt.Println("[fail] ep.configure")
		return nil, err
	}

	if err := d.saveEndpoint(id, ep); err != nil {
		fmt.Println("[fail] d.saveEndpoint")
		return nil, err
	}

	return ep, nil
}

func (d *BridgeDriver) Unlink(netid, name string, sb sandbox.Sandbox) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	ep, err := d.loadEndpoint(netid, name)
	if err != nil {
		return fmt.Errorf("No endpoint for name %q: %v", name, err)
	}

	if err := ep.deconfigure(name); err != nil {
		return err
	}

	if err := d.removeEndpoint(netid, name); err != nil {
		return err
	}

	return nil
}

func (d *BridgeDriver) saveNetwork(id string, bridge *BridgeNetwork) error {
	// FIXME allocator, address will be broken if not saved
	if err := d.setNetworkProperty(id, "bridgeInterface", bridge.bridge.Name); err != nil {
		return err
	}

	if err := d.setNetworkProperty(id, "address", bridge.network.String()); err != nil {
		return err
	}

	return nil
}

func (d *BridgeDriver) loadNetwork(id string) (*BridgeNetwork, error) {
	iface, err := d.getNetworkProperty(id, "bridgeInterface")
	if err != nil {
		return nil, err
	}

	addr, err := d.getNetworkProperty(id, "address")
	if err != nil {
		return nil, err
	}

	ip, ipNet, err := net.ParseCIDR(addr)
	ipNet.IP = ip

	return &BridgeNetwork{
		// DEMO FIXME
		//vxlan:       &netlink.Vxlan{LinkAttrs: netlink.LinkAttrs{Name: "vx" + iface}},
		bridge:      &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: iface}},
		ID:          id,
		driver:      d,
		network:     ipNet,
		ipallocator: NewIPAllocator(id, ipNet, nil, nil),
	}, nil
}

func (d *BridgeDriver) AddNetwork(id string, args []string) error {
	// FIXME this should be abstracted from the network driver

	fs := flag.NewFlagSet("simplebridge", flag.ContinueOnError)
	// FIXME need to figure out a way to prop usage
	fs.Usage = func() {}
	peer := fs.String("peer", "", "VXLan peer to contact")
	vlanid := fs.Uint("vid", 42, "VXLan VLAN ID")
	port := fs.Uint("port", 0, "VXLan Tunneling Port")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := d.createNetwork(id); err != nil {
		return err
	}

	bridge, err := d.createBridge(id, *vlanid, *port, *peer)
	if err != nil {
		return err
	}

	if err := d.saveNetwork(id, bridge); err != nil {
		return err
	}

	return nil
}

func (d *BridgeDriver) RemoveNetwork(id string) error {
	bridge, err := d.loadNetwork(id)
	if err != nil {
		return err
	}

	if err := d.removeNetwork(id); err != nil {
		return err
	}

	return bridge.destroy()
}

func (d *BridgeDriver) createBridge(id string, vlanid uint, port uint, peer string) (*BridgeNetwork, error) {
	dockerbridge := &netlink.Bridge{netlink.LinkAttrs{Name: id}}

	if err := netlink.LinkAdd(dockerbridge); err != nil {
		log.Printf("Error add bridge %#v", dockerbridge)
		return nil, err
	}

	addr, err := GetBridgeIP()
	if err != nil {
		return nil, err
	}

	if err := netlink.AddrAdd(dockerbridge, &netlink.Addr{IPNet: addr}); err != nil {
		log.Println("Error add addr bridge")
		return nil, err
	}

	if err := netlink.LinkSetUp(dockerbridge); err != nil {
		log.Println("Error up bridge")
		return nil, err
	}

	if err := setupIPTables(id, addr, true, true); err != nil {
		return nil, err
	}

	var vxlan *netlink.Vxlan

	if peer != "" {
		vxlan = &netlink.Vxlan{
			// DEMO FIXME: name collisions, better error recovery
			LinkAttrs: netlink.LinkAttrs{Name: "vx" + id},
			VxlanId:   int(vlanid),
			Group:     net.ParseIP(peer),
			Port:      int(port),
			Learning:  true,
			Proxy:     true,
			L2miss:    true,
		}

		if err := netlink.LinkAdd(vxlan); err != nil {
			log.Println("Error linkadd")
			return nil, err
		}

		if err := netlink.LinkSetMaster(vxlan, dockerbridge); err != nil {
			log.Println("Error linksetmaster")
			return nil, err
		}

		if err := netlink.LinkSetUp(vxlan); err != nil {
			log.Println("Error linksetmaster")
			return nil, err
		}
	}

	return &BridgeNetwork{
		vxlan:       vxlan,
		bridge:      dockerbridge,
		ID:          id,
		driver:      d,
		network:     addr,
		ipallocator: NewIPAllocator(id, addr, nil, nil),
	}, nil
}

func (d *BridgeDriver) destroyBridge(b *netlink.Bridge, v *netlink.Vxlan) error {
	// DEMO FIXME
	if v != nil {
		if err := netlink.LinkDel(v); err != nil {
			return err
		}
	}

	return netlink.LinkDel(b)
}

// FIXME remove last two parameters
func setupIPTables(bridgeIface string, addr net.Addr, icc, ipmasq bool) error {
	// Enable NAT

	if ipmasq {
		natArgs := []string{"POSTROUTING", "-t", "nat", "-s", addr.String(), "!", "-o", bridgeIface, "-j", "MASQUERADE"}

		if !iptables.Exists(natArgs...) {
			if output, err := iptables.Raw(append([]string{"-I"}, natArgs...)...); err != nil {
				return fmt.Errorf("Unable to enable network bridge NAT: %s", err)
			} else if len(output) != 0 {
				return &iptables.ChainError{Chain: "POSTROUTING", Output: output}
			}
		}
	}

	var (
		args       = []string{"FORWARD", "-i", bridgeIface, "-o", bridgeIface, "-j"}
		acceptArgs = append(args, "ACCEPT")
		dropArgs   = append(args, "DROP")
	)

	if !icc {
		iptables.Raw(append([]string{"-D"}, acceptArgs...)...)

		if !iptables.Exists(dropArgs...) {
			log.Debugf("Disable inter-container communication")
			if output, err := iptables.Raw(append([]string{"-I"}, dropArgs...)...); err != nil {
				return fmt.Errorf("Unable to prevent intercontainer communication: %s", err)
			} else if len(output) != 0 {
				return fmt.Errorf("Error disabling intercontainer communication: %s", output)
			}
		}
	} else {
		iptables.Raw(append([]string{"-D"}, dropArgs...)...)

		if !iptables.Exists(acceptArgs...) {
			log.Debugf("Enable inter-container communication")
			if output, err := iptables.Raw(append([]string{"-I"}, acceptArgs...)...); err != nil {
				return fmt.Errorf("Unable to allow intercontainer communication: %s", err)
			} else if len(output) != 0 {
				return fmt.Errorf("Error enabling intercontainer communication: %s", output)
			}
		}
	}

	// Accept all non-intercontainer outgoing packets
	outgoingArgs := []string{"FORWARD", "-i", bridgeIface, "!", "-o", bridgeIface, "-j", "ACCEPT"}
	if !iptables.Exists(outgoingArgs...) {
		if output, err := iptables.Raw(append([]string{"-I"}, outgoingArgs...)...); err != nil {
			return fmt.Errorf("Unable to allow outgoing packets: %s", err)
		} else if len(output) != 0 {
			return &iptables.ChainError{Chain: "FORWARD outgoing", Output: output}
		}
	}

	// Accept incoming packets for existing connections
	existingArgs := []string{"FORWARD", "-o", bridgeIface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}

	if !iptables.Exists(existingArgs...) {
		if output, err := iptables.Raw(append([]string{"-I"}, existingArgs...)...); err != nil {
			return fmt.Errorf("Unable to allow incoming packets: %s", err)
		} else if len(output) != 0 {
			return &iptables.ChainError{Chain: "FORWARD incoming", Output: output}
		}
	}
	return nil
}
