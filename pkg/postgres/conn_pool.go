package postgres

import (
	"errors"
	"sync"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/jackc/pgx"
)

type Conn interface {
	Close() (err error)
	Exec(sql string, arguments ...interface{}) (commandTag pgx.CommandTag, err error)
	Listen(channel string) (err error)
	WaitForNotification(timeout time.Duration) (*pgx.Notification, error)
}

type conn struct {
	*pgx.Conn
}

type simConn struct {
	*conn

	breakc, dropc chan struct{}
}

func newSimConn(c *conn) *simConn {
	return &simConn{
		conn:   c,
		breakc: make(chan struct{}),
		dropc:  make(chan struct{}),
	}
}

func (c *simConn) Break() {
	close(c.breakc)
}

func (c *simConn) Drop() {
	close(c.dropc)
}

type notifErr struct {
	notification *pgx.Notification
	err          error
}

func (c *simConn) WaitForNotification(timeout time.Duration) (*pgx.Notification, error) {
	nec := make(chan notifErr)

	if c.isBroken() {
		return nil, pgx.ErrDeadConn
	}

	go func() {
		notif, err := c.conn.WaitForNotification(timeout)
		nec <- notifErr{notif, err}
	}()

	select {
	case ne := <-nec:
		return ne.notification, ne.err
	case <-c.dropc:
		<-nec
		return nil, pgx.ErrNotificationTimeout
	case <-c.breakc:
		go func() { <-nec }()
		return nil, pgx.ErrDeadConn
	}
}

func (c *simConn) isBroken() bool {
	select {
	case <-c.breakc:
		return true
	default:
		return false
	}
}

type ConnPool interface {
	Acquire() (c Conn, err error)
	Close()
	Exec(sql string, arguments ...interface{}) (commandTag pgx.CommandTag, err error)
	Query(sql string, args ...interface{}) (*pgx.Rows, error)
	QueryRow(sql string, args ...interface{}) *pgx.Row
	Release(conn Conn)
}

type connPool struct {
	*pgx.ConnPool
}

func NewConnPool(config pgx.ConnPoolConfig) (*connPool, error) {
	pgxConnPool, err := pgx.NewConnPool(config)
	if err != nil {
		return nil, err
	}
	return &connPool{pgxConnPool}, nil
}

func (p *connPool) Acquire() (Conn, error) {
	pgxConn, err := p.ConnPool.Acquire()
	if err != nil {
		return nil, err
	}

	return &conn{pgxConn}, nil
}

func (p *connPool) Release(c Conn) {
	switch c := c.(type) {
	case *conn:
		p.ConnPool.Release(c.Conn)
	default:
		panic("unreleasable conn")
	}
}

type SimConnPool struct {
	*connPool

	mtx      sync.RWMutex
	simConns []*simConn
}

func NewSimConnPool(config pgx.ConnPoolConfig) (*SimConnPool, error) {
	cp, err := NewConnPool(config)
	if err != nil {
		return nil, err
	}

	return &SimConnPool{
		connPool: cp,
		simConns: make([]*simConn, 0),
	}, nil
}

func (p *SimConnPool) Acquire() (Conn, error) {
	c, err := p.connPool.Acquire()
	if err != nil {
		return nil, err
	}

	switch c := c.(type) {
	case *conn:
		sc := newSimConn(c)

		p.mtx.Lock()
		defer p.mtx.Unlock()
		p.simConns = append(p.simConns, sc)

		return sc, nil
	default:
		return nil, errors.New("acquired unknown conn")
	}
}

func (p *SimConnPool) Release(c Conn) {
	sc := c.(*simConn)
	p.deleteConn(sc)

	p.connPool.Release(sc.conn)
}

func (p *SimConnPool) deleteConn(sc *simConn) {
	p.mtx.Lock()
	defer p.mtx.Unlock()

	for i := range p.simConns {
		if p.simConns[i] == sc {
			p.simConns[0], p.simConns[i] = p.simConns[i], p.simConns[0]
			p.simConns = p.simConns[1:]
			return
		}
	}
}

func (p *SimConnPool) Drop() {
	p.mtx.RLock()
	defer p.mtx.RUnlock()

	for _, sc := range p.simConns {
		sc.Drop()
	}
}

func (p *SimConnPool) Break() {
	p.mtx.RLock()
	defer p.mtx.RUnlock()

	for _, simConn := range p.simConns {
		simConn.Break()
	}
}
