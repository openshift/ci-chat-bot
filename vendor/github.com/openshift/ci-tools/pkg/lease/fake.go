package lease

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/boskos/common"
)

type fakeClient struct {
	owner    string
	failures sets.Set[string]
	calls    *[]string
}

func NewFakeClient(owner, url string, retries int, failures sets.Set[string], calls *[]string) Client {
	if calls == nil {
		calls = &[]string{}
	}
	randId = func() string {
		return "random"
	}
	return newClient(&fakeClient{
		owner:    owner,
		failures: failures,
		calls:    calls,
	}, retries, time.Duration(0))
}

func (c *fakeClient) addCall(call string, args ...string) error {
	s := strings.Join(append([]string{call, c.owner}, args...), " ")
	if c.calls != nil {
		*c.calls = append(*c.calls, s)
	}
	if c.failures.Has(s) {
		return fmt.Errorf("injected failure %q", s)
	}
	return nil
}

func (c *fakeClient) AcquireWaitWithPriority(ctx context.Context, rtype, state, dest, requestID string) (*common.Resource, error) {
	err := c.addCall("acquire", rtype, state, dest, requestID)
	return &common.Resource{Name: fmt.Sprintf("%s_%d", rtype, len(*c.calls)-1)}, err
}

func (c *fakeClient) UpdateOne(name, dest string, _ *common.UserData) error {
	return c.addCall("updateone", name, dest, strconv.Itoa(len(*c.calls)-1))
}

func (c *fakeClient) ReleaseOne(name, dest string) error {
	return c.addCall("releaseone", name, dest)
}

func (c *fakeClient) ReleaseAll(dest string) error {
	return c.addCall("releaseall", dest)
}

func (*fakeClient) Metric(rtype string) (common.Metric, error) {
	return common.NewMetric(rtype), nil
}
