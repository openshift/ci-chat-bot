module github.com/openshift/ci-chat-bot

go 1.16

replace (
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.6.0
	github.com/bombsimon/logrusr => github.com/stevekuznetsov/logrusr v1.1.1-0.20210709145202-301b9fbb8872
	github.com/containerd/containerd => github.com/containerd/containerd v0.2.10-0.20180716142608-408d13de2fbb
	github.com/docker/docker => github.com/openshift/moby-moby v1.4.2-0.20190308215630-da810a85109d
	github.com/moby/buildkit => github.com/dmcgowan/buildkit v0.0.0-20170731200553-da2b9dc7dab9

	github.com/openshift/api => github.com/openshift/api v0.0.0-20201120165435-072a4cd8ca42
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20210730113412-1811c1b3fc0e
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20210826121606-162472d92388

	k8s.io/api => k8s.io/api v0.22.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.22.0
	k8s.io/client-go => k8s.io/client-go v0.22.0
	k8s.io/component-base => k8s.io/component-base v0.22.0
	k8s.io/kubectl => k8s.io/kubectl v0.22.0
)

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/elazarl/goproxy v0.0.0-20200315184450-1f3cb6622dad // indirect
	github.com/openshift/api v0.0.0-20210730095913-85e1d547cdee
	github.com/openshift/ci-tools v0.0.0-20220203161918-dbab1f148fc5
	github.com/openshift/client-go v3.9.0+incompatible
	github.com/sbstjn/allot v0.0.0-20161025071122-1f2349af5ccd // indirect
	github.com/sbstjn/hanu v0.1.0
	github.com/shomali11/proper v0.0.0-20190608032528-6e70a05688e7 // indirect
	github.com/shomali11/slacker v0.0.0-20200420173605-4887ab8127b6
	github.com/slack-go/slack v0.7.3
	github.com/spf13/pflag v1.0.5
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/klog v1.0.0
	k8s.io/test-infra v0.0.0-20220217031743-9703a3111217
	sigs.k8s.io/yaml v1.2.0
)
