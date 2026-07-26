package dataplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fitlab-ai/fleet/internal/model"
)

type BackendID string

type Mode string

const (
	ModeProxy Mode = "proxy"
	ModeTUN   Mode = "tun"
)

type ValidationPurpose string

const (
	ValidationRefresh ValidationPurpose = "refresh"
	ValidationStart   ValidationPurpose = "start"
	ValidationExport  ValidationPurpose = "export"
	ValidationProbe   ValidationPurpose = "probe"
)

type ResourceKind string

const (
	ResourceRuntimeLock ResourceKind = "runtime-lock"
	ResourceProcess     ResourceKind = "process"
	ResourcePort        ResourceKind = "port"
	ResourceSystemProxy ResourceKind = "system-proxy"
	ResourceTUN         ResourceKind = "tun"
	ResourceRoute       ResourceKind = "route"
	ResourceDNS         ResourceKind = "dns"
)

type ErrorCode string

const (
	CodeInvalidRequest   ErrorCode = "invalid-request"
	CodeUnsupported      ErrorCode = "unsupported"
	CodeDependency       ErrorCode = "dependency"
	CodeConfig           ErrorCode = "config"
	CodeResourceConflict ErrorCode = "resource-conflict"
	CodePermission       ErrorCode = "permission"
	CodeStart            ErrorCode = "start"
	CodeStop             ErrorCode = "stop"
	CodeProbe            ErrorCode = "probe"
	CodeStateCorrupt     ErrorCode = "state-corrupt"
	CodeOwnershipUnknown ErrorCode = "ownership-unknown"
)

type Error struct {
	Code          ErrorCode
	Operation     string
	Backend       BackendID
	PublicMessage string
	Cause         error
}

func NewError(code ErrorCode, operation string, backend BackendID, message string, cause error) *Error {
	return &Error{Code: code, Operation: operation, Backend: backend, PublicMessage: message, Cause: cause}
}

func (e *Error) Error() string {
	if e.PublicMessage != "" {
		return e.PublicMessage
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsCode(err error, code ErrorCode) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Code == code
}

type ListenEndpoint struct {
	Network string `json:"network"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

type Extension struct {
	Backend BackendID       `json:"backend"`
	Schema  string          `json:"schema"`
	Version uint32          `json:"version"`
	Payload json.RawMessage `json:"payload"`
}

type ExtensionCapability struct {
	Versions []uint32
	MaxBytes int
}

type Capabilities struct {
	Modes      []Mode
	Protocols  []string
	Resources  map[Mode][]ResourceKind
	Extensions map[string]ExtensionCapability
	Version    string
}

func (c Capabilities) Require(request ValidateRequest) error {
	if request.Purpose != ValidationRefresh && !slices.Contains(c.Modes, request.Mode) {
		return NewError(CodeUnsupported, "capabilities", request.Backend, "The selected mode is not supported", nil)
	}
	for _, node := range request.Nodes {
		if !slices.Contains(c.Protocols, strings.ToLower(node.Type)) {
			return NewError(CodeUnsupported, "capabilities", request.Backend, "The selected backend does not support this proxy protocol", nil)
		}
	}
	if request.Extension == nil {
		return nil
	}
	extension := request.Extension
	if extension.Backend != request.Backend {
		return NewError(CodeUnsupported, "capabilities", request.Backend, "The backend extension does not match the selected backend", nil)
	}
	schema, ok := c.Extensions[extension.Schema]
	if !ok || !slices.Contains(schema.Versions, extension.Version) {
		return NewError(CodeUnsupported, "capabilities", request.Backend, "The backend extension schema is not supported", nil)
	}
	if schema.MaxBytes > 0 && len(extension.Payload) > schema.MaxBytes {
		return NewError(CodeInvalidRequest, "capabilities", request.Backend, "The backend extension is too large", nil)
	}
	if !json.Valid(extension.Payload) {
		return NewError(CodeInvalidRequest, "capabilities", request.Backend, "The backend extension is invalid", nil)
	}
	return nil
}

type ValidateRequest struct {
	Backend   BackendID
	Purpose   ValidationPurpose
	Mode      Mode
	Nodes     []model.Node
	Endpoint  ListenEndpoint
	Extension *Extension
}

type RenderRequest struct {
	Backend   BackendID
	Purpose   ValidationPurpose
	Mode      Mode
	Node      model.Node
	Endpoint  ListenEndpoint
	Extension *Extension
}

type ConfigArtifact struct {
	Backend  BackendID
	Format   string
	Filename string
	Bytes    []byte
	SHA256   string
}

type StartRequest struct {
	ArtifactPath string
	RuntimeDir   string
	Mode         Mode
	LeaseID      string
	Endpoint     ListenEndpoint
	Environment  map[string]string
}

type ProcessIdentity struct {
	PID             int       `json:"pid"`
	Executable      string    `json:"executable,omitempty"`
	ArgsFingerprint string    `json:"args_fingerprint,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
}

type ResourceObservation struct {
	Kind ResourceKind `json:"kind"`
	Key  string       `json:"key"`
}

type Instance struct {
	ID        string                `json:"id"`
	Backend   BackendID             `json:"backend"`
	Mode      Mode                  `json:"mode"`
	Process   ProcessIdentity       `json:"process"`
	StartedAt time.Time             `json:"started_at"`
	Resources []ResourceObservation `json:"resources,omitempty"`
}

type ProbeKind string

const (
	ProbeInstance ProbeKind = "instance"
	ProbeOutbound ProbeKind = "outbound"
)

type ProbeRequest struct {
	Kind     ProbeKind
	Instance *Instance
	Node     *model.Node
	Endpoint ListenEndpoint
	Target   string
}

type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthStarting  HealthStatus = "starting"
	HealthStopped   HealthStatus = "stopped"
	HealthUnknown   HealthStatus = "unknown"
)

type HealthResult struct {
	Status    HealthStatus
	Latency   time.Duration
	Reason    ErrorCode
	CheckedAt time.Time
	Resources []ResourceObservation
}

type DataPlane interface {
	ID() BackendID
	Capabilities(context.Context) (Capabilities, error)
	Validate(context.Context, ValidateRequest) error
	Render(context.Context, RenderRequest) (ConfigArtifact, error)
	Start(context.Context, StartRequest) (Instance, error)
	Stop(context.Context, Instance) error
	Probe(context.Context, ProbeRequest) (HealthResult, error)
}

type Registry struct {
	defaultID BackendID
	planes    map[BackendID]DataPlane
}

func NewRegistry(defaultID BackendID, planes ...DataPlane) (*Registry, error) {
	registry := &Registry{defaultID: defaultID, planes: map[BackendID]DataPlane{}}
	for _, plane := range planes {
		if plane == nil || plane.ID() == "" {
			return nil, fmt.Errorf("dataplane: adapter ID is required")
		}
		if _, exists := registry.planes[plane.ID()]; exists {
			return nil, fmt.Errorf("dataplane: duplicate adapter %q", plane.ID())
		}
		registry.planes[plane.ID()] = plane
	}
	if _, exists := registry.planes[defaultID]; !exists {
		return nil, fmt.Errorf("dataplane: default adapter %q is not registered", defaultID)
	}
	return registry, nil
}

func (r *Registry) Configured(id BackendID) (DataPlane, error) {
	if id == "" {
		id = r.defaultID
	}
	plane, exists := r.planes[id]
	if !exists {
		return nil, NewError(CodeDependency, "registry", id, "The selected data plane is not available", nil)
	}
	return plane, nil
}

func (r *Registry) DefaultID() BackendID { return r.defaultID }
