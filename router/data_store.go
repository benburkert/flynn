package main

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/jackc/pgx"
	"github.com/flynn/flynn/router/types"
)

var ErrNotFound = errors.New("router: route not found")

type DataStore interface {
	Add(route *router.Route) error
	Set(route *router.Route) error
	Get(id string) (*router.Route, error)
	List() ([]*router.Route, error)
	Remove(id string) error
	Sync(h SyncHandler, started chan<- error)
	StopSync()
}

type DataStoreReader interface {
	Get(id string) (*router.Route, error)
	List() ([]*router.Route, error)
}

type SyncHandler interface {
	Set(route *router.Route) error
	Remove(id string) error
}

type pgDataStore struct {
	pgx *pgx.ConnPool

	routeType string
	tableName string

	doneo sync.Once
	donec chan struct{}
}

const (
	routeTypeHTTP = "http"
	routeTypeTCP  = "tcp"
	tableNameHTTP = "http_routes"
	tableNameTCP  = "tcp_routes"
)

// NewPostgresDataStore returns a DataStore that stores route information in a
// Postgres database. It uses pg_notify and a listener connection to watch for
// route changes.
func NewPostgresDataStore(routeType string, pgx *pgx.ConnPool) *pgDataStore {
	tableName := ""
	switch routeType {
	case routeTypeHTTP:
		tableName = tableNameHTTP
	case routeTypeTCP:
		tableName = tableNameTCP
	default:
		panic(fmt.Sprintf("unknown routeType: %q", routeType))
	}
	return &pgDataStore{
		pgx:       pgx,
		routeType: routeType,
		tableName: tableName,
		donec:     make(chan struct{}),
	}
}

const sqlAddRouteHTTP = `
INSERT INTO ` + tableNameHTTP + ` (parent_ref, service, domain, tls_cert, tls_key, sticky)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id, created_at, updated_at`

const sqlAddRouteTCP = `
INSERT INTO ` + tableNameTCP + ` (parent_ref, service, port)
	VALUES ($1, $2, $3)
	RETURNING id, created_at, updated_at`

func (d *pgDataStore) Add(r *router.Route) (err error) {
	switch d.tableName {
	case tableNameHTTP:
		err = d.pgx.QueryRow(
			sqlAddRouteHTTP,
			r.ParentRef,
			r.Service,
			r.Domain,
			r.TLSCert,
			r.TLSKey,
			r.Sticky,
		).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	case tableNameTCP:
		err = d.pgx.QueryRow(
			sqlAddRouteTCP,
			r.ParentRef,
			r.Service,
			r.Port,
		).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	}
	r.Type = d.routeType
	return err
}

const sqlSetRouteHTTP = `
UPDATE %s SET parent_ref = $1, service = $2, domain = $3, tls_cert = $4, tls_key = $5, sticky = $6
	WHERE id = $7 AND deleted_at IS NULL
	RETURNING updated_at`

const sqlSetRouteTCP = `
UPDATE %s SET parent_ref = $1, service = $2, port = $3
	WHERE id = $4 AND deleted_at IS NULL
	RETURNING updated_at`

func (d *pgDataStore) Set(r *router.Route) error {
	var row *pgx.Row

	switch d.tableName {
	case tableNameHTTP:
		row = d.pgx.QueryRow(
			fmt.Sprintf(sqlSetRouteHTTP, d.tableName),
			r.ParentRef,
			r.Service,
			r.Domain,
			r.TLSCert,
			r.TLSKey,
			r.Sticky,
			r.ID,
		)
	case tableNameTCP:
		row = d.pgx.QueryRow(
			fmt.Sprintf(sqlSetRouteTCP, d.tableName),
			r.ParentRef,
			r.Service,
			r.Port,
			r.ID,
		)
	}
	err := row.Scan(&r.UpdatedAt)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	r.Type = d.routeType
	return err
}

const sqlRemoveRoute = `UPDATE %s SET deleted_at = now() WHERE id = $1`

func (d *pgDataStore) Remove(id string) error {
	_, err := d.pgx.Exec(fmt.Sprintf(sqlRemoveRoute, d.tableName), id)
	return err
}

const sqlGetRoute = `SELECT %s FROM %s WHERE id = $1 AND deleted_at IS NULL`

func (d *pgDataStore) Get(id string) (*router.Route, error) {
	if id == "" {
		return nil, ErrNotFound
	}

	query := fmt.Sprintf(sqlGetRoute, d.columnNames(), d.tableName)
	row := d.pgx.QueryRow(query, id)

	r := &router.Route{}
	err := d.scanRoute(r, row)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return r, nil
}

const sqlListRoutes = `SELECT %s FROM %s WHERE deleted_at IS NULL`

func (d *pgDataStore) List() ([]*router.Route, error) {
	query := fmt.Sprintf(sqlListRoutes, d.columnNames(), d.tableName)
	rows, err := d.pgx.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []*router.Route{}
	for rows.Next() {
		r := &router.Route{}
		err := d.scanRoute(r, rows)
		if err != nil {
			return nil, err
		}

		r.Type = d.routeType
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

func (d *pgDataStore) Sync(h SyncHandler, started chan<- error) {
	idc := make(chan string)
	if err := d.startListener(idc); err != nil {
		started <- err
		return
	}
	defer d.StopSync()

	initialRoutes, err := d.List()
	if err != nil {
		started <- err
		return
	}

	for _, route := range initialRoutes {
		if err := h.Set(route); err != nil {
			started <- err
			return
		}
	}
	close(started)

	for {
		select {
		case id := <-idc:
			d.handleUpdate(h, id)
		case <-d.donec:
			for range idc {
			}
			return
		}
	}
}

func (d *pgDataStore) StopSync() {
	d.doneo.Do(func() { close(d.donec) })
}

func (d *pgDataStore) handleUpdate(h SyncHandler, id string) {
	route, err := d.Get(id)
	if err == ErrNotFound {
		if err = h.Remove(id); err != nil && err != ErrNotFound {
			// TODO(benburkert): structured logging
			log.Printf("router: sync handler remove error: %s, %s", id, err)
		}
		return
	}

	if err != nil {
		log.Printf("router: datastore error: %s, %s", id, err)
		return
	}

	if err := h.Set(route); err != nil {
		log.Printf("router: sync handler set error: %s, %s", id, err)
	}
}

func (d *pgDataStore) startListener(idc chan<- string) error {
	conn, err := d.pgx.Acquire()
	if err != nil {
		return err
	}
	if err = conn.Listen(d.tableName); err != nil {
		d.pgx.Release(conn)
		return err
	}

	go func() {
		defer unlistenAndRelease(d.pgx, conn, d.tableName)
		defer close(idc)

		for {
			select {
			case <-d.donec:
				return
			default:
			}
			notification, err := conn.WaitForNotification(time.Second)
			if err == pgx.ErrNotificationTimeout {
				continue
			}
			if err != nil {
				log.Printf("router: notifier error: %s", err)
				d.StopSync()
				return
			}

			idc <- notification.Payload
		}
	}()

	return nil
}

const (
	selectColumnsHTTP = "id, parent_ref, service, domain, sticky, tls_cert, tls_key, created_at, updated_at"
	selectColumnsTCP  = "id, parent_ref, service, port, created_at, updated_at"
)

func (d *pgDataStore) columnNames() string {
	switch d.routeType {
	case routeTypeHTTP:
		return selectColumnsHTTP
	case routeTypeTCP:
		return selectColumnsTCP
	default:
		panic(fmt.Sprintf("unknown routeType: %q", d.routeType))
	}
}

type scannable interface {
	Scan(dest ...interface{}) (err error)
}

func (d *pgDataStore) scanRoute(route *router.Route, s scannable) error {
	route.Type = d.routeType
	switch d.tableName {
	case tableNameHTTP:
		return s.Scan(
			&route.ID,
			&route.ParentRef,
			&route.Service,
			&route.Domain,
			&route.Sticky,
			&route.TLSCert,
			&route.TLSKey,
			&route.CreatedAt,
			&route.UpdatedAt,
		)
	case tableNameTCP:
		return s.Scan(
			&route.ID,
			&route.ParentRef,
			&route.Service,
			&route.Port,
			&route.CreatedAt,
			&route.UpdatedAt,
		)
	}
	panic("unknown tableName: " + d.tableName)
}

const sqlUnlisten = `UNLISTEN %s`

func unlistenAndRelease(pool *pgx.ConnPool, conn *pgx.Conn, channel string) {
	_, err := conn.Exec(fmt.Sprintf(sqlUnlisten, channel))
	if err != nil {
		conn.Close()
		return
	}
	pool.Release(conn)
}
