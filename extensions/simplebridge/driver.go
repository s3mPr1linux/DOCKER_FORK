package simplebridge

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/docker/docker/network"
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

	ep := &BridgeEndpoint{network: network, ID: name}

	if ep, err := d.loadEndpoint(id, name); ep != nil && err != nil && !replace {
		return nil, fmt.Errorf("Endpoint %q already taken", name)
	}

	if err := d.createEndpoint(id, name); err != nil {
		return nil, err
	}

	if err := ep.configure(name, s); err != nil {
		return nil, err
	}

	if err := d.saveEndpoint(id, ep); err != nil {
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
	if err := d.setNetworkProperty(id, "bridgeInterface", bridge.bridge.Name); err != nil {
		return err
	}

	return nil
}

func (d *BridgeDriver) loadNetwork(id string) (*BridgeNetwork, error) {
	iface, err := d.getNetworkProperty(id, "bridgeInterface")
	if err != nil {
		return nil, err
	}

	return &BridgeNetwork{
		bridge: &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: iface}},
		ID:     id,
		driver: d,
	}, nil
}

func (d *BridgeDriver) AddNetwork(id string) error {
	if err := d.createNetwork(id); err != nil {
		return err
	}

	bridge, err := d.createBridge(id)
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

func (d *BridgeDriver) createBridge(id string) (*BridgeNetwork, error) {
	dockerbridge := &netlink.Bridge{netlink.LinkAttrs{Name: id}}

	if err := netlink.LinkAdd(dockerbridge); err != nil {
		return nil, err
	}

	addr, err := GetBridgeIP()
	if err != nil {
		return nil, err
	}

	if err := netlink.AddrAdd(dockerbridge, &netlink.Addr{IPNet: addr}); err != nil {
		return nil, err
	}

	if err := netlink.LinkSetUp(dockerbridge); err != nil {
		return nil, err
	}

	return &BridgeNetwork{
		bridge:  dockerbridge,
		ID:      id,
		driver:  d,
		network: addr,
	}, nil
}
