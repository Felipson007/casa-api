package aiconnectors

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/tenancy"
)

type fakeRepo struct {
	conns []Connector
	err   error
}

func (f fakeRepo) List(context.Context) ([]Connector, error) { return f.conns, f.err }
func (f fakeRepo) Get(context.Context, uuid.UUID) (*Connector, error) {
	return nil, nil
}
func (f fakeRepo) Create(context.Context, Connector) error { return nil }
func (f fakeRepo) Update(context.Context, Connector) error { return nil }
func (f fakeRepo) Delete(context.Context, uuid.UUID) error { return nil }

func TestProviderPresets(t *testing.T) {
	if !ProviderOpenAI.Valid() || !ProviderOmniRoute.Valid() {
		t.Fatal("known providers should be valid")
	}
	if Provider("NOPE").Valid() {
		t.Fatal("unknown provider should be invalid")
	}
	if got := ProviderOpenAI.defaultBaseURL(); got != "https://api.openai.com/v1" {
		t.Fatalf("openai base url = %q", got)
	}
	if got := ProviderOmniRoute.defaultBaseURL(); got != "" {
		t.Fatalf("omniroute must require a tenant-supplied url, got %q", got)
	}
	if got := ProviderAnthropic.defaultAuthStyle(); got != pandora.AuthBearer {
		t.Fatalf("anthropic auth style = %q, want BEARER", got)
	}
}

func TestResolverPicksLowestPriorityEnabled(t *testing.T) {
	repo := fakeRepo{conns: []Connector{
		{ID: uuid.New(), Provider: ProviderOpenAI, Enabled: true, Priority: 200, ChatModel: "high"},
		{ID: uuid.New(), Provider: ProviderAnthropic, Enabled: false, Priority: 1, ChatModel: "disabled"},
		{ID: uuid.New(), Provider: ProviderGroq, Enabled: true, Priority: 10, ChatModel: "low"},
	}}
	r := NewResolver(repo, nil, tenancy.Tenant{})
	ctx := tenancy.WithTenantID(context.Background(), uuid.New())
	c, err := r.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.ChatModel != "low" {
		t.Fatalf("chosen chat model = %q, want low (lowest priority enabled)", c.ChatModel)
	}
	if c.BaseURL != ProviderGroq.defaultBaseURL() {
		t.Fatalf("base url = %q, want the groq preset", c.BaseURL)
	}
}

func TestResolverNoConnectorGetsNothing(t *testing.T) {
	r := NewResolver(fakeRepo{}, nil, tenancy.Tenant{})
	ctx := tenancy.WithTenantID(context.Background(), uuid.New())
	c, err := r.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.BaseURL != "" {
		t.Fatalf("a tenant with no connector must get no provider (no fallback), got %q", c.BaseURL)
	}
}

func TestResolverNoTenantUsesDefaultTenantConnector(t *testing.T) {
	def := tenancy.Tenant{ID: uuid.New(), SchemaName: "tenant_owner"}
	repo := fakeRepo{conns: []Connector{
		{ID: uuid.New(), Provider: ProviderGroq, Enabled: true, Priority: 10, ChatModel: "op"},
	}}
	r := NewResolver(repo, nil, def)
	c, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.ChatModel != "op" {
		t.Fatalf("no-tenant resolution should use the default tenant's connector, got %q", c.ChatModel)
	}
}

func TestResolverNoTenantNoDefaultGetsNothing(t *testing.T) {
	r := NewResolver(fakeRepo{}, nil, tenancy.Tenant{})
	c, err := r.Resolve(context.Background())
	if err != nil || c.BaseURL != "" {
		t.Fatalf("Resolve = (%+v, %v), want empty", c, err)
	}
}
