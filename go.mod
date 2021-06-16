module github.com/openshift/ci-chat-bot

go 1.16

replace (
	k8s.io/api => k8s.io/api v0.21.1
	k8s.io/apimachinery => k8s.io/apimachinery v0.21.1
	k8s.io/client-go => k8s.io/client-go v0.21.1
)

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/elazarl/goproxy v0.0.0-20200315184450-1f3cb6622dad // indirect
	github.com/openshift/api v0.0.0-20200326160804-ecb9283fe820
	github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0
	github.com/sbstjn/allot v0.0.0-20161025071122-1f2349af5ccd // indirect
	github.com/sbstjn/hanu v0.1.0
	github.com/shomali11/proper v0.0.0-20190608032528-6e70a05688e7 // indirect
	github.com/shomali11/slacker v0.0.0-20200420173605-4887ab8127b6
	github.com/slack-go/slack v0.6.4
	github.com/spf13/pflag v1.0.5
	gopkg.in/fsnotify.v1 v1.4.7
	k8s.io/api v0.21.1
	k8s.io/apimachinery v0.21.1
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/klog v1.0.0
	k8s.io/test-infra v0.0.0-20210616143559-b29abc8d656e
	sigs.k8s.io/yaml v1.2.0
)
