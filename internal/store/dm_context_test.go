package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

type dmContextTestConnector struct{ address string }

func (c *dmContextTestConnector) Driver() driver.Driver { return nil }
func (c *dmContextTestConnector) Connect(ctx context.Context) (driver.Conn, error) {
	scope, ok := ctx.Value(dmDialScopeKey{}).(*sqlServerContextDialer)
	if !ok {
		return nil, errors.New("missing bounded DM dial scope")
	}
	socket, err := scope.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return nil, err
	}
	if _, err = socket.Read(make([]byte, 1)); err != nil {
		return nil, err
	}
	return &dmContextTestConn{socket}, nil
}

type dmContextTestConn struct{ net.Conn }

func (c *dmContextTestConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *dmContextTestConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

func TestDMContextConnectorBoundsUncooperativeHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	connector := &dmContextConnector{Connector: &dmContextTestConnector{listener.Addr().String()}, lifetime: ctx}
	start := time.Now()
	if conn, err := connector.Connect(ctx); err == nil {
		conn.Close()
		t.Fatal("unresponsive handshake succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("DM handshake escaped deadline")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("DM scope did not dial peer")
	}
}
func TestDMContextConnectorOrdinaryConnectionSurvivesConnectCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			conn.Write([]byte("a"))
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	connector := &dmContextConnector{Connector: &dmContextTestConnector{listener.Addr().String()}}
	conn, err := connector.Connect(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer conn.Close()
	server := <-accepted
	defer server.Close()
	cancel()
	time.Sleep(100 * time.Millisecond)
	socket := conn.(*dmContextTestConn)
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() { server.Write([]byte("b")) }()
	value := make([]byte, 1)
	if _, err := socket.Read(value); err != nil || string(value) != "b" {
		t.Fatal("ordinary DM connection retained obsolete context", err)
	}
}

// Models the vendor's ep.connect: the caller context is deliberately discarded.
type dmDiscardContextConnector struct {
	slot    *dmDialSlot
	address string
}

func (c *dmDiscardContextConnector) Driver() driver.Driver { return nil }
func (c *dmDiscardContextConnector) Connect(context.Context) (driver.Conn, error) {
	socket, err := c.slot.dial(c.address)
	if err != nil {
		return nil, err
	}
	if _, err = socket.Read(make([]byte, 1)); err != nil {
		return nil, err
	}
	return &dmContextTestConn{socket}, nil
}
func TestDMContextSlotsSurviveVendorContextLoss(t *testing.T) {
	pool := newDMDialSlotPool(1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	native := newDMScopedConnector(pool, nil, func(id int, _ bool) (driver.Connector, error) {
		return &dmDiscardContextConnector{pool.slots[id], listener.Addr().String()}, nil
	})
	connector := &dmContextConnector{Connector: native, lifetime: ctx}
	start := time.Now()
	if _, err := connector.Connect(ctx); err == nil {
		t.Fatal("context-discarding driver escaped deadline")
	}
	if time.Since(start) > time.Second {
		t.Fatal("context loss defeated total deadline")
	}
	connector.Close()
	if len(pool.available) != 1 {
		t.Fatal("failed pool retained slot after Close")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("peer not reached")
	}
}
func TestDMContextSlotQueueCancellationAndDBCloseRelease(t *testing.T) {
	pool := newDMDialSlotPool(1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			conn.Write([]byte("a"))
			accepted <- conn
		}
	}()
	factory := func(id int, _ bool) (driver.Connector, error) {
		return &dmDiscardContextConnector{pool.slots[id], listener.Addr().String()}, nil
	}
	owner := newDMScopedConnector(pool, nil, factory)
	db := sql.OpenDB(&bindingConnector{Connector: &dmContextConnector{Connector: owner}, dialect: DM})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := db.PingContext(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	server := <-accepted
	defer server.Close()
	waiting := &dmContextConnector{Connector: newDMScopedConnector(pool, nil, factory)}
	waitCtx, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	if _, err := waiting.Connect(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("slot wait ignored deadline", err)
	}
	waiting.Close()
	if len(pool.available) != 0 {
		t.Fatal("waiting pool stole owner's slot")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if len(pool.available) != 1 {
		t.Fatal("sql.DB.Close did not release connector slot")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if len(pool.available) != 1 {
		t.Fatal("duplicate Close released twice")
	}
}
func TestDMContextSlotsDoNotShareCancellation(t *testing.T) {
	pool := newDMDialSlotPool(2)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithTimeout(context.Background(), time.Second)
	defer cancelB()
	slotA, _ := pool.acquire(ctxA)
	defer pool.release(slotA)
	slotB, _ := pool.acquire(ctxB)
	defer pool.release(slotB)
	slotA.bind(&sqlServerContextDialer{lifetime: ctxA})
	slotB.bind(&sqlServerContextDialer{lifetime: ctxB})
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	peers := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, e := listener.Accept()
			if e == nil {
				peers <- c
			}
		}
	}()
	a, e := slotA.dial(listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	serverA := <-peers
	defer serverA.Close()
	b, e := slotB.dial(listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	serverB := <-peers
	defer serverB.Close()
	cancelA()
	if _, e := a.Read(make([]byte, 1)); e == nil {
		t.Fatal("cancelled slot remained open")
	}
	go func() { serverB.Write([]byte("b")) }()
	value := make([]byte, 1)
	if _, e := b.Read(value); e != nil || string(value) != "b" {
		t.Fatal("one slot cancelled another", e)
	}
}
func TestDMLiveConnectionSurvivesConnectCancellation(t *testing.T) {
	dsn := os.Getenv("PIPEWRIGHT_TEST_DM_DSN")
	if dsn == "" || dmConnectorFactory == nil {
		t.Skip("private DM driver and dedicated test DSN required")
	}
	capacity := len(dmTransportSlots.available)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := ConnectContext(ctx, OpenConfig{Driver: "dm", DSN: dsn})
	cancel()
	if err != nil {
		t.Fatal("private DM connection failed")
	}
	queryCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	var one int
	if err := db.DB.QueryRowContext(queryCtx, "SELECT 1 FROM DUAL").Scan(&one); err != nil || one != 1 {
		db.Close()
		t.Fatal("DM pooled connection did not survive connection-context cancellation")
	}
	if err := db.Close(); err != nil {
		t.Fatal("DM connection cleanup failed")
	}
	if len(dmTransportSlots.available) != capacity {
		t.Fatal("DM pool close retained a dial slot")
	}
}
