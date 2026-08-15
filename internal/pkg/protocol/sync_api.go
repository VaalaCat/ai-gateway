package protocol

type APIPermissionGrant struct {
	Resource   string `json:"resource"`
	ResourceID uint   `json:"resource_id"`
	Action     string `json:"action"`
}

type SyncedAPIRole struct {
	ID          uint                 `json:"id"`
	Name        string               `json:"name"`
	Permissions []APIPermissionGrant `json:"permissions"`
}

type APIRoleSet struct {
	RoleIDs []uint `json:"role_ids"`
}

const APIFullSyncSnapshotContractV1 = "api_full_sync_v1"

type SyncedAPIService struct {
	ID            uint   `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	ConsumesQuota bool   `json:"consumes_quota"`
	Status        int    `json:"status"`
}

type SyncedAPIRoute struct {
	ID                    uint     `json:"id"`
	ServiceID             uint     `json:"service_id"`
	BackendID             uint     `json:"backend_id"`
	Slug                  string   `json:"slug"`
	Protocols             []string `json:"protocols"`
	AllowedMethods        []string `json:"allowed_methods"`
	WebSocketSubprotocols []string `json:"websocket_subprotocols"`
	UpstreamPath          string   `json:"upstream_path"`
	ForwardSubpath        bool     `json:"forward_subpath"`
	Status                int      `json:"status"`
}

type APIUpstreamCredential struct {
	BearerToken   string `json:"bearer_token,omitempty"`
	HeaderName    string `json:"header_name,omitempty"`
	HeaderValue   string `json:"header_value,omitempty"`
	QueryName     string `json:"query_name,omitempty"`
	QueryValue    string `json:"query_value,omitempty"`
	BasicUsername string `json:"basic_username,omitempty"`
	BasicPassword string `json:"basic_password,omitempty"`
}

type SyncedAPIUpstream struct {
	ID             uint                  `json:"id"`
	BackendID      uint                  `json:"backend_id"`
	Name           string                `json:"name"`
	BaseURL        string                `json:"base_url"`
	AuthType       string                `json:"auth_type"`
	Credential     APIUpstreamCredential `json:"credential"`
	HeaderOverride map[string]string     `json:"header_override"`
	ProxyURL       string                `json:"proxy_url"`
	Priority       int                   `json:"priority"`
	Weight         int                   `json:"weight"`
	Status         int                   `json:"status"`
}

type APIRoleSetFetchRequest struct {
	PrincipalID uint `json:"principal_id"`
}

type APIRoleSetFetchResult struct {
	PrincipalID uint       `json:"principal_id"`
	Exists      bool       `json:"exists"`
	RoleSet     APIRoleSet `json:"role_set"`
}

type APIRoleSetInvalidate struct {
	PrincipalID uint `json:"principal_id"`
}
