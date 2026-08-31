module github.com/openshift/ci-chat-bot

go 1.26.4

replace (
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.6.0
	github.com/apcera/gssapi => github.com/openshift/gssapi v0.0.0-20161010215902-5fb4217df13b
	github.com/bombsimon/logrusr => github.com/stevekuznetsov/logrusr v1.1.1-0.20210709145202-301b9fbb8872
	github.com/docker/docker => github.com/openshift/moby-moby v1.4.2-0.20190308215630-da810a85109d
	github.com/moby/buildkit => github.com/dmcgowan/buildkit v0.0.0-20170731200553-da2b9dc7dab9
	github.com/openshift/api => github.com/openshift/api v0.0.0-20260825094607-13a84dedc5a3
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20260603140539-6892dc3e1ffc
	github.com/openshift/hive => github.com/openshift/hive v0.0.0-20240904155057-b6cdaa9cb317
	github.com/openshift/hive/apis => github.com/openshift/hive/apis v0.0.0-20240904155057-b6cdaa9cb317
	github.com/openshift/installer => github.com/openshift/installer v1.5.0-alpha.2.0.20260827095017-c8d299965fc1
	k8s.io/component-base => k8s.io/component-base v0.36.3
	k8s.io/kubectl => k8s.io/kubectl v0.36.4
)

// from hive; needed because otherwise v12.0.0 is picked up as a more recent version
replace (
	k8s.io/apimachinery => k8s.io/apimachinery v0.36.4
	k8s.io/client-go => k8s.io/client-go v0.36.4
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.24.1
)

// from hive
// from installer
replace (
	github.com/metal3-io/baremetal-operator => github.com/openshift/baremetal-operator v0.0.0-20231128154154-6736c9b9c6c8
	github.com/metal3-io/baremetal-operator/apis => github.com/openshift/baremetal-operator/apis v0.0.0-20231128154154-6736c9b9c6c8
	github.com/metal3-io/baremetal-operator/pkg/hardwareutils => github.com/openshift/baremetal-operator/pkg/hardwareutils v0.0.0-20231128154154-6736c9b9c6c8
	k8s.io/cloud-provider-vsphere => github.com/openshift/cloud-provider-vsphere v1.19.1-0.20240626105621-6464d0bb4928
)

require (
	cloud.google.com/go/bigquery v1.81.0
	github.com/adrg/xdg v0.5.3
	github.com/blang/semver v3.5.1+incompatible
	github.com/onsi/ginkgo/v2 v2.28.3
	github.com/onsi/gomega v1.42.1
	github.com/openshift-eng/cyborg-data/go v0.0.0-20260901124137-30bc39816344
	github.com/openshift-online/ocm-sdk-go v0.1.459
	github.com/openshift/api v0.0.0-20260825094607-13a84dedc5a3
	github.com/openshift/build-machinery-go v0.0.0-20250530140348-dc5b2804eeee
	github.com/openshift/ci-tools v0.0.0-20260401181544-dbe3f838b8a0
	github.com/openshift/client-go v3.9.0+incompatible
	github.com/openshift/hive v0.0.0-00010101000000-000000000000
	github.com/openshift/oc v0.0.0-alpha.0.0.20230410203846-b68ac7c39057
	github.com/operator-framework/operator-registry v1.51.0
	github.com/sirupsen/logrus v1.10.1
	github.com/slack-go/slack v0.23.1
	github.com/spf13/afero v1.15.0
	github.com/spf13/pflag v1.0.10
	go.podman.io/image/v5 v5.40.0
	golang.org/x/mod v0.38.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.36.4
	k8s.io/apimachinery v0.36.4
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	open-cluster-management.io/api v0.14.0
	sigs.k8s.io/controller-runtime v0.24.1
	sigs.k8s.io/prow v0.0.0-20260829024204-88f56c0e89c8
	sigs.k8s.io/yaml v1.6.0
)

require (
	cel.dev/expr v0.25.3 // indirect
	cloud.google.com/go/monitoring v1.30.0 // indirect
	cloud.google.com/go/secretmanager v1.21.0 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.36.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.60.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.60.0 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/PaesslerAG/jsonpath v0.1.1 // indirect
	github.com/andygrunwald/go-jira v1.17.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/apache/arrow/go/v15 v15.0.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.2.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.7 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/containerd/containerd/api v1.11.1 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/platforms v1.0.0-rc.4 // indirect
	github.com/containers/common v0.62.0 // indirect
	github.com/containers/image/v5 v5.34.0 // indirect
	github.com/containers/libtrust v0.0.0-20230121012942-c1716e8a8d01 // indirect
	github.com/containers/ocicrypt v1.3.0 // indirect
	github.com/containers/storage v1.57.1 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/coreos/ignition/v2 v2.25.0 // indirect
	github.com/coreos/vcontext v0.0.0-20230201181013-d72178a18687 // indirect
	github.com/docker/go-events v0.0.0-20260608200158-dbf6103125a4 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.39.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/analysis v0.23.0 // indirect
	github.com/go-openapi/errors v0.22.4 // indirect
	github.com/go-openapi/loads v0.22.0 // indirect
	github.com/go-openapi/spec v0.21.0 // indirect
	github.com/go-openapi/strfmt v0.25.0 // indirect
	github.com/go-openapi/swag/cmdutils v0.27.1 // indirect
	github.com/go-openapi/swag/conv v0.27.1 // indirect
	github.com/go-openapi/swag/fileutils v0.27.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.27.1 // indirect
	github.com/go-openapi/swag/loading v0.27.1 // indirect
	github.com/go-openapi/swag/mangling v0.27.1 // indirect
	github.com/go-openapi/swag/netutils v0.27.1 // indirect
	github.com/go-openapi/swag/pools v0.27.1 // indirect
	github.com/go-openapi/swag/stringutils v0.27.1 // indirect
	github.com/go-openapi/swag/typeutils v0.27.1 // indirect
	github.com/go-openapi/swag/yamlutils v0.27.1 // indirect
	github.com/go-openapi/validate v0.24.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/flatbuffers v23.5.26+incompatible // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/kdomanski/iso9660 v0.2.1 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/metal3-io/baremetal-operator/apis v0.4.0 // indirect
	github.com/metal3-io/baremetal-operator/pkg/hardwareutils v0.4.0 // indirect
	github.com/moby/sys/capability v0.4.0 // indirect
	github.com/moby/sys/user v0.4.1 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/nutanix-cloud-native/prism-go-client v0.8.0 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/openshift/cluster-autoscaler-operator v0.0.1-0.20241204142113-43631b045675 // indirect
	github.com/otiai10/mint v1.6.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.18 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/spiffe/go-spiffe/v2 v2.8.1 // indirect
	github.com/vincent-petithory/dataurl v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/zeebo/xxh3 v1.0.2 // indirect
	go.mongodb.org/mongo-driver v1.17.6 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/prometheus v0.66.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.46.0 // indirect
	go.podman.io/storage v1.63.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/telemetry v0.0.0-20260708182218-49f421fb7959 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	k8s.io/apiextensions-apiserver v0.36.3 // indirect
	sigs.k8s.io/cluster-api v1.13.4 // indirect
	sigs.k8s.io/cluster-api-provider-azure v1.24.2 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.1 // indirect
)

require (
	cloud.google.com/go/auth v0.23.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4 // indirect
	github.com/99designs/keyring v1.2.2 // indirect
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20240806141605-e8a1dd7889d6 // indirect
	github.com/AlecAivazis/survey/v2 v2.3.7 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Microsoft/hcsshim v0.14.1 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/RangelReale/osincli v0.0.0-20160924135400-fababb0555f2 // indirect
	github.com/alessio/shellescape v1.4.2 // indirect
	github.com/alexbrainman/sspi v0.0.0-20180613141037-e580b900e9f5 // indirect
	github.com/apcera/gssapi v0.0.0-00010101000000-000000000000 // indirect
	github.com/aws/aws-sdk-go-v2 v1.43.7
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.18 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.38 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.37 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.39 // indirect
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.56.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.292.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/iam v1.53.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.38 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.39 // indirect
	github.com/aws/aws-sdk-go-v2/service/organizations v1.27.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.107.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.41.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/servicequotas v1.34.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.7 // indirect
	github.com/aws/smithy-go v1.27.8 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/bombsimon/logrusr/v4 v4.1.0 // indirect
	github.com/briandowns/spinner v1.11.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cjwagner/httpcache v0.0.0-20230907212505-d4841bbad466 // indirect
	github.com/containerd/cgroups/v3 v3.1.3 // indirect
	github.com/containerd/containerd v1.7.33 // indirect
	github.com/containerd/continuity v0.5.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/ttrpc v1.2.8 // indirect
	github.com/containerd/typeurl/v2 v2.3.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/cli v29.5.3+incompatible // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker v28.3.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.7 // indirect
	github.com/docker/go-connections v0.7.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dvsekhvalnov/jose2go v1.8.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.0 // indirect
	github.com/go-git/go-git/v5 v5.19.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/godbus/dbus v0.0.0-20190726142602-4481cbc300e2 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/golang-migrate/migrate/v4 v4.18.2 // indirect
	github.com/golang/glog v1.2.5 // indirect
	github.com/google/cel-go v0.29.2 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.21 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/gsterjov/go-libsecret v0.0.0-20161001094733-a6f4afe4910c // indirect
	github.com/h2non/filetype v1.1.3 // indirect
	github.com/h2non/go-is-svg v0.0.0-20160927212452-35e8c4b0612c // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/heptio/velero v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/joelanford/ignore v0.1.1 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.44 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/microcosm-cc/bluemonday v1.0.26 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/sequential v0.7.0 // indirect
	github.com/mtibben/percent v0.2.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/openshift-online/ocm-common v0.0.21 // indirect
	github.com/openshift/cluster-api-provider-ovirt v0.1.1-0.20220323121149-e3f2850dd519 // indirect
	github.com/openshift/custom-resource-status v1.1.3-0.20220503160415-f2fdb4999d87 // indirect
	github.com/openshift/hive/apis v0.0.0
	github.com/openshift/installer v1.4.17
	github.com/openshift/library-go v0.0.0-20260505113324-de46cb8e2ddc // indirect
	github.com/otiai10/copy v1.14.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.74.0 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/zalando/go-keyring v0.2.3 // indirect
	github.com/zgalor/weberr v0.6.0 // indirect
	gitlab.com/c0b/go-ordered-json v0.0.0-20201030195603-febf46534d5a // indirect
	go.etcd.io/bbolt v1.4.3 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.71.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/exp v0.0.0-20260727155853-b88d891fe743 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	k8s.io/apiserver v0.36.3 // indirect
	k8s.io/cluster-registry v0.0.6 // indirect
	k8s.io/kube-aggregator v0.35.1 // indirect
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.34.0 // indirect
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/iam v1.13.0 // indirect
	cloud.google.com/go/storage v1.65.1 // indirect
	github.com/GoogleCloudPlatform/testgrid v0.0.176 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bwmarrin/snowflake v0.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clarketm/json v1.17.1 // indirect
	github.com/danwakefield/fnmatch v0.0.0-20160403171240-cbb64ac3d964 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/denormal/go-gitignore v0.0.0-20180930084346-ae8ad1d07817 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch v5.9.11+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/fgprof v0.9.5 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/fvbommel/sortorder v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/go-openapi/jsonreference v1.0.0 // indirect
	github.com/go-openapi/swag v0.27.1 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/gomodule/redigo v1.9.3 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-cmp v0.7.0
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20260402051712-545e8a4df936 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/google/wire v0.7.0 // indirect
	github.com/googleapis/gax-go/v2 v2.24.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mattn/go-zglob v0.0.6 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/openshift/rosa v1.2.53
	github.com/operator-framework/api v0.29.0
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_golang v1.24.1
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/shurcooL/githubv4 v0.0.0-20210725200734-83ba7b4c9228 // indirect
	github.com/shurcooL/graphql v0.0.0-20181231061246-d48a9a75455f // indirect
	github.com/tektoncd/pipeline v1.15.1 // indirect
	github.com/trivago/tgo v1.0.7 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go4.org v0.0.0-20201209231011-d4a079459e60 // indirect
	gocloud.dev v0.46.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	gomodules.xyz/jsonpatch/v2 v2.5.0 // indirect
	google.golang.org/api v0.294.0
	google.golang.org/genproto v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/fsnotify.v1 v1.4.7 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/component-base v0.36.3 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad // indirect
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3 // indirect
	knative.dev/pkg v0.0.0-20260622140654-39ebae2ee2dc // indirect
	sigs.k8s.io/boskos v0.0.0-20240624145324-1e4de26c366a // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
)
