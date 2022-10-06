package lease

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	boskos "sigs.k8s.io/boskos/client"
	"sigs.k8s.io/boskos/common"
)

const (
	freeState   = "free"
	leasedState = "leased"
)

type boskosClient interface {
	AcquireWaitWithPriority(ctx context.Context, rtype, state, dest, requestID string) (*common.Resource, error)
	UpdateOne(name, dest string, _ *common.UserData) error
	ReleaseOne(name, dest string) error
	ReleaseAll(dest string) error
	Metric(rtype string) (common.Metric, error)
}

var ErrNotFound = boskos.ErrNotFound

type Metrics struct {
	Free, Leased int
}

// Client manages resource leases, acquiring, releasing, and keeping them
// updated.
type Client interface {
	// Acquire leases `n` resources and returns the lease names.
	// Will block until resources are available or 150m pass, `n` must be > 0.
	// `ctx` can be used to abort the operation, `cancel` is called if any
	// subsequent updates to the lease fail.
	Acquire(rtype string, n uint, ctx context.Context, cancel context.CancelFunc) ([]string, error)
	// Heartbeat updates all leases. It calls the cancellation function of each
	// lease it fails to update.
	Heartbeat() error
	// Release ends one lease by name.
	Release(name string) error
	// ReleaseAll ends all leases and returns the names of those that were
	// successfully released.
	ReleaseAll() ([]string, error)
	// Metrics queries the states of a particular resource, for informational
	// purposes.
	Metrics(rtype string) (Metrics, error)
}

// NewClient creates a client that leases resources with the specified owner.
func NewClient(owner, url, username string, passwordGetter func() []byte, retries int, acquireTimeout time.Duration) (Client, error) {
	randId = func() string {
		return strconv.Itoa(rand.Int())
	}
	c, err := boskos.NewClientWithPasswordGetter(owner, url, username, passwordGetter)
	if err != nil {
		return nil, err
	}
	return newClient(c, retries, acquireTimeout), nil
}

// for test mocking
var randId func() string

func newClient(boskos boskosClient, retries int, acquireTimeout time.Duration) Client {
	return &client{
		boskos:         boskos,
		retries:        retries,
		acquireTimeout: acquireTimeout,
		leases:         make(map[string]*lease),
	}
}

type client struct {
	sync.RWMutex
	boskos         boskosClient
	retries        int
	acquireTimeout time.Duration
	leases         map[string]*lease
}

type lease struct {
	updateFailures int
	// cancel holds a cancellation function for steps that depend on leases
	// being active; we must cancel this when we encounter errors to tie the
	// lifetime of the downstream user routines to those of the leases they
	// require
	cancel context.CancelFunc
}

func (c *client) Acquire(rtype string, n uint, ctx context.Context, cancel context.CancelFunc) ([]string, error) {
	var cancelAcquire context.CancelFunc
	ctx, cancelAcquire = context.WithTimeout(ctx, c.acquireTimeout)
	defer cancelAcquire()
	var ret []string
	// TODO `m` processes may fight for the last `m * n` remaining leases
	for i := uint(0); i < n; i++ {
		r, err := c.boskos.AcquireWaitWithPriority(ctx, rtype, freeState, leasedState, randId())
		if err != nil {
			return nil, err
		}
		c.Lock()
		c.leases[r.Name] = &lease{cancel: cancel}
		c.Unlock()
		ret = append(ret, r.Name)
	}
	return ret, nil
}

func (c *client) Heartbeat() error {
	c.Lock()
	defer c.Unlock()
	var errs []error
	for name, lease := range c.leases {
		err := c.boskos.UpdateOne(name, leasedState, nil)
		if err == nil {
			c.leases[name].updateFailures = 0
			continue
		}
		logrus.WithError(err).Warnf("Failed to update lease %q", name)
		if lease.updateFailures != c.retries {
			c.leases[name].updateFailures++
			continue
		}
		errs = append(errs, fmt.Errorf("exceeded number of retries for lease %q", name))
		lease.cancel()
		delete(c.leases, name)
	}
	return utilerrors.NewAggregate(errs)
}

func (c *client) Release(name string) error {
	c.Lock()
	defer c.Unlock()
	if err := c.boskos.ReleaseOne(name, freeState); err != nil {
		return err
	}
	delete(c.leases, name)
	return nil
}

func (c *client) ReleaseAll() ([]string, error) {
	c.Lock()
	defer c.Unlock()
	var ret []string
	var errs []error
	for l := range c.leases {
		ret = append(ret, l)
		if err := c.boskos.ReleaseOne(l, freeState); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(c.leases, l)
	}
	return ret, utilerrors.NewAggregate(errs)
}

func (c *client) Metrics(rtype string) (Metrics, error) {
	metrics, err := c.boskos.Metric(rtype)
	if err != nil {
		return Metrics{}, err
	}
	return Metrics{
		Free:   metrics.Current[freeState],
		Leased: metrics.Current[leasedState],
	}, nil
}
