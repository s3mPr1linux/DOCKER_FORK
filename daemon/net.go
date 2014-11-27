package daemon

import (
	"github.com/docker/docker/core"
	"github.com/docker/docker/engine"
)

func (d *Daemon) CmdNetCreate(job *engine.Job) engine.Status {
	if len(job.Args) != 1 {
		return job.Errorf("usage: %s NAME", job.Name)
	}

	// FIXME What do we do with user provided name?
	// Store in Service? Store in NetController?
	netw, err := d.extensions.Networks().NewNetwork()
	if err != nil {
		return job.Error(err)
	}
	job.Printf("%v\n", netw.Id())
	return engine.StatusOK
}

func (d *Daemon) CmdNetLs(job *engine.Job) engine.Status {
	netw := d.extensions.Networks().ListNetworks()
	table := engine.NewTable("Name", len(netw))
	for _, netid := range netw {
		item := &engine.Env{}
		item.Set("ID", string(netid))
	}

	if _, err := table.WriteTo(job.Stdout); err != nil {
		return job.Error(err)
	}
	return engine.StatusOK
}

func (d *Daemon) CmdNetRm(job *engine.Job) engine.Status {
	if len(job.Args) != 1 {
		return job.Errorf("usage: %s NAME", job.Name)
	}

	if err := d.extensions.Networks().RemoveNetwork(core.DID(job.Args[0])); err != nil {
		return job.Error(err)
	}
	return engine.StatusOK
}

func (d *Daemon) CmdNetJoin(job *engine.Job) engine.Status {
	if len(job.Args) != 3 {
		return job.Errorf("usage: %s NETWORK CONTAINER NAME", job.Name)
	}

	net, err := d.extensions.Networks().GetNetwork(core.DID(job.Args[0]))
	if err != nil {
		return job.Error(err)
	}

	// FIXME The provided CONTAINER could be the 'user facing ID'. but not
	// necessarily the sandbox ID itself: we're keeping things simple herengine.
	sandbox, err := d.extensions.Sandboxes().Get(core.DID(job.Args[1]))
	if err != nil {
		return job.Error(err)
	}

	if _, err := net.Link(sandbox, job.Args[2], false); err != nil {
		return job.Error(err)
	}
	return engine.StatusOK
}

func (d *Daemon) CmdNetLeave(job *engine.Job) engine.Status {
	if len(job.Args) != 2 {
		return job.Errorf("usage: %s NETWORK NAME", job.Name)
	}

	net, err := d.extensions.Networks().GetNetwork(core.DID(job.Args[0]))
	if err != nil {
		return job.Error(err)
	}

	// FIXME: Network.Unlink should give access to the sandbox, so that the
	// driver can do cleanup.
	if err := net.Unlink(job.Args[1]); err != nil {
		return job.Error(err)
	}
	return engine.StatusOK
}

func (d *Daemon) CmdNetImport(job *engine.Job) engine.Status {
	if len(job.Args) != 1 {
		return job.Errorf("usage: %s NAME", job.Name)
	}
	// FIXME
	return engine.StatusOK
}

func (d *Daemon) CmdNetExport(job *engine.Job) engine.Status {
	if len(job.Args) != 1 {
		return job.Errorf("usage: %s NAME", job.Name)
	}
	// FIXME
	return engine.StatusOK
}
