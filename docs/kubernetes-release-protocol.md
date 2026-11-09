# Private Kubernetes tool protocol v2

The product entry point is `cmd/pipewright-kube-tool`. The shared wire types are
`internal/kubeprotocol/protocol.go`; the guarded credential holder is
`internal/kubeagent`, and the host adapter is `internal/kubernetes/release_runtime.go`.

The image is assembled with `build/kubernetes-tool/Dockerfile` from verified
Linux helper, kubectl and Helm binaries. Helm is fixed to v4.3.1. The amd64
Helm binary checksum is fixed in the Dockerfile; other architectures must supply
the independently verified official binary checksum explicitly. BASE_IMAGE must
be an approved repository@sha256 reference. The resulting immutable image digest,
architecture, nonroot UID, paths and protocol are stored in a v2 administrator
profile; a v1 grant/profile never grants v2 permissions.

The controller creates a dedicated container with a read-only root, nonroot UID,
no capabilities, no host mounts/ports/network, no logs, fixed CPU/memory/pid limits,
and a 64 MiB private noexec/nosuid/nodev tmpfs. It verifies swap/core policy,
Docker/SSH identity, exact tool image and client versions before sending secrets.

`serve` exposes a mode-0600 Unix socket inside that tmpfs and a loopback-only API
gateway. `rpc` accepts one bounded JSON frame on stdin and writes only a private
JSON reply. The host uses Docker exec with argv; tokens and documents never enter
argv, environment variables, Docker logs or public API responses.

Commands: ping, init, upload, download, forget, read, dry-run, write, poll, permit,
helm-start, helm-poll, helm-output. Input and output streams use ordered 128 KiB chunks with
size and private digest checks. Large replies are chunked without increasing the
existing 1 MiB SSH/private-command output ceiling.

The credential holder validates upstream TLS, never uses environment proxies or
redirects, and whitelists exact resource paths and GVR/scope. Controller-created
Pod/ReplicaSet/EndpointSlice reads require explicit related-read capabilities and
are filtered inside the private gateway by owner UID before leaving it. A plain
namespace list, secret reveal, exec, logs, attach, port-forward or token API is
not an implicit consequence of a deployment grant.

Every actual write waits for the host's persisted unknown boundary and a
single-use permit bound to the private request digest/action ID with at most ten
seconds of lifetime. The host rechecks ownership, runtime identity and current
policy before permit issuance. A lost response leaves the durable action unknown;
it cannot be replayed by renaming a request or obtaining another permit.

Helm receives only the loopback kubeconfig, a frozen local Chart archive/values,
fixed argv and a clean environment. Plugin paths, dynamic dependencies, proxy
configuration, arbitrary post-renderers and raw CLI output are unavailable. Every
Helm API write passes through the same permit path; request/receipt facts and
health verification are separate from the Helm process exit code.

This file describes implementation wiring. It is not evidence that the complete
suite, every Helm path or either real execution environment has passed acceptance.

Helm 4 also uses SSA PATCH for new objects. New Helm resources therefore require
explicit create **and** patch capabilities. When the host has verified an exact
NotFound with no frozen baseline, it persists a create action and issues a
`createOnly` permit. The helper replaces that single SSA creation with an exact
named POST, using the controller-approved body. A concurrent same-name object
then produces a conflict instead of being adopted. Existing objects retain
UID/resourceVersion checks. The original pending-request digest and single-use
permit remain mandatory; old grants are not expanded. Helper writes are serialized
until the preceding API result has been published, so parallel Helm requests
cannot overtake durable receipt processing.

Read-only Helm planning uses server dry-run with its OpenAPI client validation
disabled; the host still validates resources and performs controlled API dry-runs.
Packaged new CRDs may supplement **only the CLI planning discovery view**, after
an exact upstream NotFound and explicit CRD/custom-resource grants. Existing CRDs
are not replaced or upgraded. RPC discovery, reads and health checks always use
the actual API. A planned CRD must become Established and its served API/schema
must be observed before custom-resource writes are permitted.

These behaviors follow the pinned
[Helm 4.3.1 resource client](https://github.com/helm/helm/blob/v4.3.1/pkg/kube/client.go)
and [install preparation](https://github.com/helm/helm/blob/v4.3.1/pkg/action/install.go).


New Helm plans freeze mutationVersion=1. Optional protocol2 `ping.capabilities`
contains `permit-cas-merge-v1` on a compatible tool; new Helm preparation and
execution require it before business writes. Old plans and ordinary modes retain
the existing protocol2 behavior when this field is absent.

The sealed `helm-owned-post-cas-merge-v1` exception applies only to a verified
prior Pipewright POST of the exact resource UID, same project/environment,
connection/credential/context/executor/cluster and exact Helm release
name/namespace/storage anchor. The latest action and creation origin remain
bound to their sealed parent plans and receipts. Unknown outcomes, mismatched
ownership, managed-field drift, and any nonstatus SSA Apply manager prevent this
exception. External objects continue to use SSA without forcing conflicts.

Generic and custom-resource arrays compare positionally and compile atomically; a `name` field never implies a merge key. Native keyed handling is limited to exact supported GVK/PodSpec paths and v1 Service spec.ports (port/protocol). Owned env/initContainers follow the desired sequence while external members retain their live order and relation to surviving owned anchors. Conflicting order constraints reject before dispatch. Unknown CRD map/set semantics are not inferred, including nested native-field-name lookalikes. Partial image overlays preserve unrelated native declarations; arbitrary named custom arrays are replaced as complete arrays only with an unchanged owned baseline.

Helm still submits its full approved rendered SSA request. After validating that
input exactly, the host can issue optional `permit.contentType` and
`permit.fieldManager` with its uploaded frozen merge body, current UID/RV and
persisted action marker. The helper accepts only the fixed manager and exact
merge content type for this narrowed operation; it rechecks body identity and
explicit patch permissions. The original pending request digest, bounded expiry
and single-use action permit remain mandatory. Absent optional fields preserve
the historical wire behavior; create-only remains an atomic POST, not a merge.

Helm supplemental storage probes retain their approved check-N step identities
and may consume WFFC PVCs before waiting for Bound. Final Bound and actual probe
completion remain required. Completed hook deletions must have a ready same-UID
creation receipt and observed absence before later probes can omit that hook
from readiness checks; accepted deletion alone is insufficient.

New health snapshots exclude only an undeclared positive integer Deployment
revision annotation proven by exact FieldsV1 ownership under a main-resource
or status-subresource Update from kube-controller-manager or k3s. The status
entry must hold the exact root annotation path, not merely status fields. Explicit annotations, unknown managers,
spec fields and identity checks remain enforced. Historical Expected snapshots
that already froze this annotation retain their original comparison.


Standard client-go transport `timeout` query values are accepted only as one
positive canonical Go duration, at most one minute and 32 characters. The actual
query and original pending digest retain that value. Existing HTTP client,
lease and worker deadlines are not extended, and all resource/path/GVR/owner,
force, query-key and write-permit checks remain enforced. Helm planning ignores
only this validated transport value when matching its CLI-only approved CRD
catalog; any other query continues through real API handling without a planning
supplement. Runtime API reads never use the planning catalog.


Helm 4.3.1 install and upgrade consistently set
`--disable-openapi-validation` during both planning and execution. The bounded
private gateway uses JSON; client-go OpenAPISchema explicitly requests and
decodes protobuf, so its local OpenAPI v2 decoder is incompatible with this
transport. This flag disables only CLI-side OpenAPI validation. Server-side
`dryRun=All&fieldValidation=Strict`, exact approved/admitted fields, resource
identity, frozen request digest and single-use mutation permits remain required.
No arbitrary Accept header forwarding or increased response limit is introduced.
Helm 4.3.1 rollback already calls its resource builder with validation disabled
and has no corresponding CLI flag; its approved revision/UID/write gates remain
unchanged. See the pinned [client-go discovery implementation](https://github.com/kubernetes/client-go/blob/v0.36.5/discovery/discovery_client.go),
[Helm install](https://github.com/helm/helm/blob/v4.3.1/pkg/action/install.go),
[upgrade](https://github.com/helm/helm/blob/v4.3.1/pkg/action/upgrade.go) and
[rollback](https://github.com/helm/helm/blob/v4.3.1/pkg/action/rollback.go).


The private Helm CLI proxy advertises `helm-storage-protobuf-json-v1`; new Helm
mutationVersion=1 plans require it as well as `permit-cas-merge-v1`. Missing
either capability fails with the existing tool-version incompatibility before
business mutation. The pinned Helm typed storage client writes protobuf and
exposes no supported CLI content-type override. Only proxy POST/PUT of an
explicitly granted Helm storage revision Secret can be decoded by the official
api/apimachinery v0.36.5 serializer, whose scheme registers only v1 Secret.
The exact granted namespace, anchor/revision and Secret type are checked; an
omitted typed-client body namespace is bound to that exact authorized URL.
Raw input and canonical JSON each retain the original1MiB budget. Only the
canonical JSON enters the existing identity checks, pending digest, host review
and single-use permit; opaque protobuf is never forwarded. Raw RPC and other
resources/methods do not gain protobuf support. Responses remain bounded JSON.
There is no negotiation retry or replay after an uncertain/accepted write.

Network-deny probes observe either an explicit TCP dial ECONNREFUSED or expiry
of their own dial deadline. DNS, no-route/host-unreachable, reset, permission,
generic text errors and parent cancellation remain failures, as does a successful
dial. The sealed controller plan requires a same-namespace network-allow control
with the exact same IP:port; its actual Job/owned Pod health and same-action UID
receipt must be verified before dispatching the negative probe. This is a paired
reachability check, not an assertion of which network component caused refusal.
No arbitrary network-error success or new grant is introduced.


### Helm 4.3.1 hook transport compatibility

A new approved hook plan requires `helm-hook-openapi-protobuf-v1` as well as the
existing CAS and Helm-storage JSON capabilities, checked before business writes.
Only the private CLI proxy can negotiate exact `GET /openapi/v2` with
`application/com.github.proto-openapi.spec.v2@v1.0+protobuf`. The same pinned API
connection returns the native `application/com.github.proto-openapi.spec.v2.v1.0+protobuf`
representation with the existing 4 MiB and 10-second limits. JSON RPC, arbitrary
headers, credentials and all mutation permissions remain unchanged.

For an explicitly granted Job/Pod hook, the proxy may fill the missing TypeMeta
on the CLI's exact one-field JSON `DeleteOptions` (`propagationPolicy: Background`).
An absent object is read-only; an existing object still requires the controller's
single-use permit and exact UID/resourceVersion deletion preconditions.

Helm remains on `--wait=hookOnly`, with fixed official client-go environment
`KUBE_FEATURE_WatchListClient=false`. Ordinary resources use the host's actual
health/probe gates, including WFFC consumers. A CLI-only collection watch without
a selector is split into exact `metadata.name` watches for all explicitly granted
`get`/`watch`/`helm-hook` Job or Pod names in that collection. No unscoped upstream
watch is sent. At most 128 names, eight concurrent 1-second watches, a 25-second
aggregate deadline, 4096 aggregate events and 4 MiB aggregate output are allowed;
the lease/caller can end it sooner. Partial HTTP/stream failures fail closed and
cancel every worker. Events retain their opaque resourceVersion values. Public
RPC still rejects selector-free watch; ordinary collection reads do not gain
permissions. No general-resource legacy waiter is enabled, avoiding a PVC wait
before the host can create its approved storage consumer.

### CoreDNS non-preempting candidate

New CoreDNS maintenance plans freeze a first ephemeral cluster-scoped
`scheduling.k8s.io/v1 PriorityClass` named `<candidatePrefix>-nonpreempting`, with
`value: 0`, `globalDefault: false`, and `preemptionPolicy: Never`. The candidate
Pod template uses that class and `Never`, with no inherited priority. The exact
class must be absent at preparation and dispatch; an existing class is never
borrowed. An explicit exact resource grant needs `get`, `create`, `delete`, and
`coredns`. The Kubernetes identity separately needs native create and exact named
get/delete permissions. No implicit cluster privilege is added.

Before candidate Deployment or live Corefile mutation, the live class UID,
policy and action marker must match this release's verified create receipt.
Class create/delete use the same sealed plan, action ledger, unknown-outcome fence
and UID/resourceVersion cleanup as other ephemeral resources. Reverse cleanup
removes the candidate before its class. Historical plans without the new optional
class identity retain their frozen semantics. Only this API type's absent versus
false `globalDefault` JSON representation is equivalent; true or policy drift is
rejected, and generic declared-field matching is unchanged.

### DaemonSet revision proof v1

Newly prepared native apps/v1 DaemonSet resources freeze optional
`daemonSetProofVersion: 1`. The exact DaemonSet anchor requires explicit
`related-controllerrevisions` plus owned Pod reads. Before business writes,
preparation verifies the helper's `daemonset-controllerrevision-read-v1`
capability and native namespace ControllerRevision list access. Every page is
bound to apps/v1 ControllerRevisionList; only the exact DaemonSet controller
owner UID/name/namespace passes the private gateway. No revision write, named
revision read, Node read or public namespace collection is inferred.

The host requires one owned history whose Data.spec.template exactly equals
the admitted live template, removing only the native `$patch: replace`
directive. It binds that history's native hash label to each Pod and checks
the deterministic native toleration/target-node-affinity projection. Neither
hash recreation nor revision-number guessing proves the current template.
ControllerRevision data never enters public results. Absent optional versions
retain historical sealed-plan/hash semantics; unsupported versions fail closed.
