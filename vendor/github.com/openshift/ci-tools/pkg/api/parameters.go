package api

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"github.com/sirupsen/logrus"
)

// +k8s:deepcopy-gen=false
type ErrParamNotFound struct {
	param string
}

func (e *ErrParamNotFound) Is(err error) bool {
	_, ok := err.(*ErrParamNotFound)
	return ok
}

func (e *ErrParamNotFound) Error() string {
	return "param \"" + e.param + "\" not found"
}

// +k8s:deepcopy-gen=false
type ErrParamTypeMismatch struct {
	want string
	got  string
}

func (e *ErrParamTypeMismatch) Is(err error) bool {
	_, ok := err.(*ErrParamTypeMismatch)
	return ok
}

func (e *ErrParamTypeMismatch) Error() string {
	return "param types mismatch: type " + e.want + " expected but got " + e.got
}

// Parameters allows a step to read values set by other steps.
// +k8s:deepcopy-gen=false
type Parameters interface {
	Has(name string) bool
	HasInput(name string) bool
	Get(name string) (any, error)
	GetString(name string) (string, error)
}

// +k8s:deepcopy-gen=false
type DeferredParameters struct {
	lock   sync.Mutex
	params Parameters
	fns    map[string]func() (any, error)
	values map[string]any
}

func NewDeferredParameters(params Parameters) *DeferredParameters {
	return &DeferredParameters{
		params: params,
		fns:    make(map[string]func() (any, error)),
		values: make(map[string]any),
	}
}

func (p *DeferredParameters) Map() (map[string]any, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	m := make(map[string]any, len(p.fns))
	for k, fn := range p.fns {
		if v, ok := p.values[k]; ok {
			m[k] = v
			continue
		}
		v, err := fn()
		if err != nil {
			return nil, fmt.Errorf("could not lazily evaluate deferred parameter %q: %w", k, err)
		}
		p.values[k] = v
		m[k] = v
	}
	return m, nil
}

func (p *DeferredParameters) Set(name string, value any) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.fns[name]; ok {
		logrus.Warnf("Ignoring dynamic parameter %q, already set", name)
		return
	}
	if _, ok := p.values[name]; ok {
		logrus.Warnf("Ignoring parameter %q, already set", name)
		return
	}
	p.values[name] = value
}

func (p *DeferredParameters) Add(name string, fn func() (any, error)) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.fns[name]; ok {
		logrus.Warnf("Overriding parameter %q", name)
	}
	p.fns[name] = fn
}

// HasInput returns true if the named parameter is an input from outside the graph, rather
// than provided either by the graph caller or another node.
func (p *DeferredParameters) HasInput(name string) bool {
	if p.hasInput(name) {
		return true
	}
	return p.params != nil && p.params.HasInput(name)
}

func (p *DeferredParameters) hasInput(name string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := os.LookupEnv(name)
	return ok
}

// Has returns true if the named parameter exists. Use HasInput if you need to know whether
// the value comes from outside the graph.
func (p *DeferredParameters) Has(name string) bool {
	if p.has(name) {
		return true
	}
	if p.params != nil && p.params.Has(name) {
		return true
	}
	_, ok := os.LookupEnv(name)
	return ok
}

func (p *DeferredParameters) has(name string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := p.fns[name]
	return ok
}

// Get retrieves the parameter `name` only if it's a string. It defaults to ""
// if the paramenter has not been found or is not a string.
func (p *DeferredParameters) GetString(name string) (string, error) {
	ret, err := p.Get(name)
	if err != nil {
		if !errors.Is(err, &ErrParamNotFound{}) {
			return "", err
		}
		return "", nil
	}

	if retStr, ok := ret.(string); ok && retStr != "" {
		return retStr, nil
	}

	return "", nil
}

// Get retrieves the parameter `name` from the current parameters or, recursively,
// from the inner ones.
func (p *DeferredParameters) Get(name string) (any, error) {
	var err error

	ret, err := p.get(name)
	if err == nil {
		return ret, nil
	}
	if !errors.Is(err, &ErrParamNotFound{}) {
		return nil, err
	}

	if p.params != nil {
		if ret, err = p.params.Get(name); err == nil {
			return ret, nil
		}
	}

	return nil, err
}

// Get retrieves the parameter `name` from the current parameters or from the env.
func (p *DeferredParameters) get(name string) (any, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if value, ok := p.values[name]; ok {
		return value, nil
	}

	if value, ok := os.LookupEnv(name); ok {
		p.values[name] = value
		return value, nil
	}

	if fn, ok := p.fns[name]; ok {
		value, err := fn()
		if err != nil {
			return nil, fmt.Errorf("could not lazily evaluate deferred parameter %q: %w", name, err)
		}

		p.values[name] = value
		return value, nil
	}

	return nil, &ErrParamNotFound{param: name}
}

func GetParamTyped[T any](params Parameters, name string) (T, error) {
	var zero T
	raw, err := params.Get(name)
	if err != nil {
		return zero, err
	}

	// The param exists but it's untyped: we have an untyped nil here.
	// Return the zero of T since we have now way to compare T's type
	// with an untyped nil.
	if raw == nil {
		return zero, nil
	}

	value, ok := raw.(T)
	if !ok {
		return zero, &ErrParamTypeMismatch{
			want: reflect.TypeFor[T]().String(),
			got:  reflect.TypeOf(raw).String(),
		}
	}

	return value, nil
}
