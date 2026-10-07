package opschat

func (s *Service) publicSession(c Session) Session {
	c.Title = s.scrub(c.Title, 320)
	c.Draft = s.scrub(c.Draft, 8<<10)
	return c
}

func (s *Service) publicEntry(e Entry) Entry {
	e.Text = s.masker.ScrubTruncated(e.Text)
	return e
}

// Inspect decoded fields, not JSON bytes: escapes must not hide a known secret.
func (s *Service) secretArgs(a ToolArgs) bool {
	for _, value := range []string{a.Container, a.Unit, a.Action} {
		if s.masker.Scrub(value) != value {
			return true
		}
	}
	return false
}

func (s *Service) publicCall(c Call) Call {
	c.Args.Container = s.scrub(c.Args.Container, 1024)
	c.Args.Unit = s.scrub(c.Args.Unit, 1024)
	c.Args.Action = s.scrub(c.Args.Action, 1024)
	c.Object = s.scrub(c.Object, 1024)
	c.ServerName = s.scrub(c.ServerName, 1024)
	c.Output = s.scrub(c.Output, 64<<10)
	c.Error = s.scrub(c.Error, 1024)
	if c.Resources != nil {
		r := *c.Resources
		r.Disks = append([]DiskResource(nil), r.Disks...)
		for i := range r.Disks {
			r.Disks[i].Filesystem = s.scrub(r.Disks[i].Filesystem, 1024)
			r.Disks[i].Mount = s.scrub(r.Disks[i].Mount, 1024)
		}
		c.Resources = &r
	}
	return c
}

func (s *Service) publicConfirmation(c Confirmation) Confirmation {
	c.Calls = append([]BoundCall(nil), c.Calls...)
	for i := range c.Calls {
		c.Calls[i].Object = s.scrub(c.Calls[i].Object, 1024)
	}
	return c
}
