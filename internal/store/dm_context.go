package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
)

// The private adapter's single registered dialer retrieves this per-connection
// scope. No credentials or request-bound callbacks enter a global registry.
type dmDialScopeKey struct{}
type dmProbeLifetimeKey struct{}

type dmContextConnector struct {
	driver.Connector
	lifetime context.Context
}

func (c *dmContextConnector) Connect(ctx context.Context) (driver.Conn, error) {
	lifetime := ctx
	if c.lifetime != nil {
		lifetime = c.lifetime
	}
	// This shared transport guard is also used by SQL Server's connector.
	dialer := &sqlServerContextDialer{lifetime: lifetime}
	scoped := context.WithValue(ctx, dmDialScopeKey{}, dialer)
	scoped = context.WithValue(scoped, dmProbeLifetimeKey{}, c.lifetime != nil)
	conn, err := c.Connector.Connect(scoped)
	connected := err == nil
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = lifetime.Err()
	}
	dialer.finish(err == nil, c.lifetime != nil)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		// Some drivers return a typed nil through driver.Conn on failure.
		if connected && conn != nil {
			conn.Close()
		}
		return nil, err
	}
	return conn, nil
}

func (c *dmContextConnector) Close() error {
	if closer, ok := c.Connector.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// DM discards caller contexts in its endpoint-group implementation, including
// single endpoints. Register a fixed number of stable dial hooks, and lease one
// to each pool for its lifetime rather than leaking per-request registrations.
type dmDialSlot struct {
	id    int
	mu    sync.Mutex
	scope *sqlServerContextDialer
}

func (s *dmDialSlot) bind(scope *sqlServerContextDialer) { s.mu.Lock(); s.scope = scope; s.mu.Unlock() }
func (s *dmDialSlot) dial(address string) (net.Conn, error) {
	s.mu.Lock()
	scope := s.scope
	s.mu.Unlock()
	if scope == nil || scope.lifetime.Err() != nil {
		return nil, errors.New("DM connection context unavailable")
	}
	return scope.DialContext(scope.lifetime, "tcp", address)
}

type dmDialSlotPool struct {
	available chan *dmDialSlot
	slots     []*dmDialSlot
}

func newDMDialSlotPool(size int) *dmDialSlotPool {
	pool := &dmDialSlotPool{available: make(chan *dmDialSlot, size)}
	for i := 0; i < size; i++ {
		slot := &dmDialSlot{id: i}
		pool.slots = append(pool.slots, slot)
		pool.available <- slot
	}
	return pool
}
func (p *dmDialSlotPool) acquire(ctx context.Context) (*dmDialSlot, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-p.available:
		if err := ctx.Err(); err != nil {
			p.available <- slot
			return nil, err
		}
		return slot, nil
	}
}
func (p *dmDialSlotPool) release(slot *dmDialSlot) { slot.bind(nil); p.available <- slot }

var dmTransportSlots = newDMDialSlotPool(64)

// The factory lives in our private adapter; it receives a bounded registered
// dial-slot name. It never exposes or copies any proprietary driver source.
type dmScopedConnector struct {
	gate       chan struct{}
	pool       *dmDialSlotPool
	factory    func(int, bool) (driver.Connector, error)
	underlying driver.Connector
	slot       *dmDialSlot
	driver     driver.Driver
	closed     bool
}

func newDMScopedConnector(pool *dmDialSlotPool, drv driver.Driver, factory func(int, bool) (driver.Connector, error)) *dmScopedConnector {
	c := &dmScopedConnector{gate: make(chan struct{}, 1), pool: pool, driver: drv, factory: factory}
	c.gate <- struct{}{}
	return c
}
func (c *dmScopedConnector) Driver() driver.Driver { return c.driver }
func (c *dmScopedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, driver.ErrBadConn
	}
	scope, ok := ctx.Value(dmDialScopeKey{}).(*sqlServerContextDialer)
	if !ok {
		return nil, errors.New("DM connection scope missing")
	}
	if c.slot == nil {
		slot, err := c.pool.acquire(ctx)
		if err != nil {
			return nil, err
		}
		probe, _ := ctx.Value(dmProbeLifetimeKey{}).(bool)
		underlying, err := c.factory(slot.id, probe)
		if err != nil {
			c.pool.release(slot)
			return nil, err
		}
		c.slot, c.underlying = slot, underlying
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.slot.bind(scope)
	defer c.slot.bind(nil)
	return c.underlying.Connect(ctx)
}
func (c *dmScopedConnector) Close() error {
	<-c.gate
	defer func() { c.gate <- struct{}{} }()
	if c.closed {
		return nil
	}
	c.closed = true
	var err error
	if closer, ok := c.underlying.(io.Closer); ok {
		err = closer.Close()
	}
	if c.slot != nil {
		c.pool.release(c.slot)
		c.slot = nil
	}
	return err
}
