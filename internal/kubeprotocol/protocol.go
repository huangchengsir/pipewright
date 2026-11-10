// Package kubeprotocol defines the private stdin/Unix-socket protocol between
// Pipewright's controller and its immutable Kubernetes tool container. No type
// in this package is a public HTTP API, audit value, or user log message.
package kubeprotocol

const Version = 2
const PermitCASMergeCapability = "permit-cas-merge-v1"
const HelmStorageJSONCapability = "helm-storage-protobuf-json-v1"
const HelmHookOpenAPICapability = "helm-hook-openapi-protobuf-v1"
const HelmHookWatchResyncCapability = "helm-hook-watch-resync-v1"
const DaemonSetRevisionReadCapability = "daemonset-controllerrevision-read-v1"
const MaxChunkBytes = 128 << 10
const MaxInputBytes = 16 << 20
const MaxResponseBytes = 4 << 20
const SocketPath = "/run/pipewright/kube-tool.sock"

type Resource struct {
	Group     string   `json:"group"`
	Version   string   `json:"version"`
	Resource  string   `json:"resource"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace,omitempty"`
	Name      string   `json:"name"`
	Actions   []string `json:"actions"`
}
type Init struct {
	Kubeconfig  []byte     `json:"kubeconfig"`
	Resources   []Resource `json:"resources"`
	HelmPath    string     `json:"helmPath"`
	HelmVersion string     `json:"helmVersion"`
	Deadline    int64      `json:"deadline"`
}
type Request struct {
	ContentType string `json:"contentType,omitempty"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Query       string `json:"query,omitempty"`
	Body        []byte `json:"body,omitempty"`
}
type PendingWrite struct {
	ID      string  `json:"id"`
	Request Request `json:"request"`
	Digest  string  `json:"digest"`
}
type Permit struct {
	ContentType  string `json:"contentType,omitempty"`
	FieldManager string `json:"fieldManager,omitempty"`
	CreateOnly   bool   `json:"createOnly,omitempty"`
	BodyStream   string `json:"bodyStream,omitempty"`
	RequestID    string `json:"requestId"`
	ActionID     string `json:"actionId"`
	Digest       string `json:"digest"`
	Expires      int64  `json:"expires"`
	Allow        bool   `json:"allow"`
}
type APIResult struct {
	RequestID string `json:"requestId,omitempty"`
	ActionID  string `json:"actionId,omitempty"`
	Status    int    `json:"status"`
	Body      []byte `json:"body,omitempty"`
	Code      string `json:"code,omitempty"`
}
type HelmTask struct {
	Action         string `json:"action"`
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
	ChartStream    string `json:"chartStream,omitempty"`
	ValuesStream   string `json:"valuesStream,omitempty"`
	Revision       int    `json:"revision,omitempty"`
	Hooks          bool   `json:"hooks,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type Frame struct {
	Version  int       `json:"version"`
	Command  string    `json:"command"`
	StreamID string    `json:"streamId,omitempty"`
	Index    int       `json:"index,omitempty"`
	Count    int       `json:"count,omitempty"`
	Size     int       `json:"size,omitempty"`
	Digest   string    `json:"digest,omitempty"`
	Data     []byte    `json:"data,omitempty"`
	Init     *Init     `json:"init,omitempty"`
	Request  *Request  `json:"request,omitempty"`
	Permit   *Permit   `json:"permit,omitempty"`
	Helm     *HelmTask `json:"helm,omitempty"`
}
type Reply struct {
	Capabilities []string      `json:"capabilities,omitempty"`
	StreamID     string        `json:"streamId,omitempty"`
	Size         int           `json:"size,omitempty"`
	Count        int           `json:"count,omitempty"`
	Digest       string        `json:"digest,omitempty"`
	Version      int           `json:"version"`
	Code         string        `json:"code"`
	Data         []byte        `json:"data,omitempty"`
	Pending      *PendingWrite `json:"pending,omitempty"`
	Result       *APIResult    `json:"result,omitempty"`
	TaskID       string        `json:"taskId,omitempty"`
	Done         bool          `json:"done,omitempty"`
	ExitCode     int           `json:"exitCode,omitempty"`
	Revision     int           `json:"revision,omitempty"`
	Gateway      string        `json:"gateway,omitempty"`
}
