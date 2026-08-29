package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kamrul1157024/helios/internal/store"
)

// registration is a provider with its capabilities already resolved.
//
// The resolution happens here, once, rather than by type assertion at each
// call site. That is deliberate. A type assertion asks "does this Go type
// implement Resumer", which is the wrong question for any provider kind where
// one Go type serves many different agents — it would implement the interface
// for all of them or none. Resolving into fields lets a future kind fill them
// from whatever it knows, and no call site changes.
type registration struct {
	p Provider

	// Each is nil when the provider does not offer it.
	resume      Resumer
	hooks       Hooker
	installer   HookInstaller
	actor       Actor
	moder       Moder
	models      ModelLister
	transcriber Transcriber
	discoverer  Discoverer
	titler      Titler
	small       SmallModel
	narrator    Narrator
	commander   Commander
	queuer      Queuer
	screen      ScreenWatcher
}

var (
	mu        sync.RWMutex
	providers = map[string]*registration{}
	order     []string
)

// Register adds a provider and resolves its capabilities.
//
// Safe to call at any time, not only at start-up: a provider that is
// discovered rather than compiled in cannot register from init().
func Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("provider: nil provider")
	}
	info := p.Info()
	if info.ID == "" {
		return fmt.Errorf("provider: empty ID")
	}
	if info.ID != strings.ToLower(info.ID) {
		return fmt.Errorf("provider %q: ID must be lower-case", info.ID)
	}

	reg := &registration{p: p}
	reg.resume, _ = p.(Resumer)
	reg.hooks, _ = p.(Hooker)
	reg.installer, _ = p.(HookInstaller)
	reg.actor, _ = p.(Actor)
	reg.moder, _ = p.(Moder)
	reg.models, _ = p.(ModelLister)
	reg.transcriber, _ = p.(Transcriber)
	reg.discoverer, _ = p.(Discoverer)
	reg.titler, _ = p.(Titler)
	reg.small, _ = p.(SmallModel)
	reg.narrator, _ = p.(Narrator)
	reg.commander, _ = p.(Commander)
	reg.queuer, _ = p.(Queuer)
	reg.screen, _ = p.(ScreenWatcher)

	mu.Lock()
	defer mu.Unlock()
	if _, dup := providers[info.ID]; dup {
		return fmt.Errorf("provider %q: already registered", info.ID)
	}
	providers[info.ID] = reg
	order = append(order, info.ID)
	return nil
}

// MustRegister panics on a duplicate or malformed provider. For compiled-in
// providers, where a failure is a programming error rather than bad input.
func MustRegister(p Provider) {
	if err := Register(p); err != nil {
		panic(err)
	}
}

// Deregister removes a provider. A reloaded provider replaces itself with
// Deregister then Register.
func Deregister(id string) {
	mu.Lock()
	defer mu.Unlock()
	delete(providers, id)
	for i, existing := range order {
		if existing == id {
			order = append(order[:i], order[i+1:]...)
			break
		}
	}
}

func lookup(id string) *registration {
	mu.RLock()
	defer mu.RUnlock()
	return providers[id]
}

// Get returns a provider by ID.
func Get(id string) (Provider, bool) {
	if reg := lookup(id); reg != nil {
		return reg.p, true
	}
	return nil, false
}

// All returns every provider, in registration order.
func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(order))
	for _, id := range order {
		if reg := providers[id]; reg != nil {
			out = append(out, reg.p)
		}
	}
	return out
}

// Infos returns every provider's identity, for the API.
func Infos() []Info {
	out := []Info{}
	for _, p := range All() {
		out = append(out, p.Info())
	}
	return out
}

// ==================== Capability accessors ====================
//
// Each returns nil when the provider does not offer the capability, or does
// not exist. Every caller must handle nil by degrading, never by erroring:
// that property is what lets a provider implement two methods and still be
// useful.

func ResumerFor(id string) Resumer {
	if reg := lookup(id); reg != nil {
		return reg.resume
	}
	return nil
}

func HookerFor(id string) Hooker {
	if reg := lookup(id); reg != nil {
		return reg.hooks
	}
	return nil
}

func InstallerFor(id string) HookInstaller {
	if reg := lookup(id); reg != nil {
		return reg.installer
	}
	return nil
}

func ActorFor(id string) Actor {
	if reg := lookup(id); reg != nil {
		return reg.actor
	}
	return nil
}

func ModerFor(id string) Moder {
	if reg := lookup(id); reg != nil {
		return reg.moder
	}
	return nil
}

func ModelListerFor(id string) ModelLister {
	if reg := lookup(id); reg != nil {
		return reg.models
	}
	return nil
}

func TranscriberFor(id string) Transcriber {
	if reg := lookup(id); reg != nil {
		return reg.transcriber
	}
	return nil
}

func DiscovererFor(id string) Discoverer {
	if reg := lookup(id); reg != nil {
		return reg.discoverer
	}
	return nil
}

func TitlerFor(id string) Titler {
	if reg := lookup(id); reg != nil {
		return reg.titler
	}
	return nil
}

func SmallModelFor(id string) SmallModel {
	if reg := lookup(id); reg != nil {
		return reg.small
	}
	return nil
}

func QueuerFor(id string) Queuer {
	if reg := lookup(id); reg != nil {
		return reg.queuer
	}
	return nil
}

func ScreenWatcherFor(id string) ScreenWatcher {
	if reg := lookup(id); reg != nil {
		return reg.screen
	}
	return nil
}

// ==================== Derived views ====================

// Capabilities is what a client needs to know about a provider, derived from
// the interfaces it implements rather than declared.
type Capabilities struct {
	Resume      bool `json:"resume"`
	Hooks       bool `json:"hooks"`
	Transcript  bool `json:"transcript"`
	Titles      bool `json:"titles"`
	Discovery   bool `json:"discovery"`
	PromptQueue bool `json:"prompt_queue"`
	// PermissionCards is whether this provider can raise something a client
	// must answer. Derived from its action routes, not from a flag.
	PermissionCards bool `json:"permission_cards"`
}

// CapabilitiesOf reports what a provider supports.
func CapabilitiesOf(id string) Capabilities {
	reg := lookup(id)
	if reg == nil {
		return Capabilities{}
	}
	caps := Capabilities{
		Resume:      reg.resume != nil,
		Hooks:       reg.hooks != nil,
		Transcript:  reg.transcriber != nil,
		Titles:      reg.titler != nil,
		Discovery:   reg.discoverer != nil,
		PromptQueue: reg.queuer != nil,
	}
	if reg.actor != nil {
		for _, route := range reg.actor.ActionRoutes() {
			if route.Blocking {
				caps.PermissionCards = true
				break
			}
		}
	}
	return caps
}

// PermissionModes returns a provider's modes, or nil when it has none.
func PermissionModes(id string) []string {
	if m := ModerFor(id); m != nil {
		return m.PermissionModes()
	}
	return nil
}

// ValidMode reports whether a provider would accept a mode. False for a
// provider with no modes at all, which is what callers want: there is nothing
// valid to set.
func ValidMode(id, mode string) bool {
	if m := ModerFor(id); m != nil {
		return m.ValidMode(mode)
	}
	return false
}

// HookHandlerFor resolves a dotted hook key — "claude.permission" — to its
// handler.
//
// The provider ID is the first segment and the rest is the route, so a
// provider owns its own namespace and two providers cannot collide.
func HookHandlerFor(key string) HookHandler {
	id, route, ok := strings.Cut(key, ".")
	if !ok {
		return nil
	}
	h := HookerFor(id)
	if h == nil {
		return nil
	}
	// Routes are registered with slashes, the key arrives with dots.
	return h.HookRoutes()[strings.ReplaceAll(route, ".", "/")]
}

// ActionHandlerFor resolves a notification type to the handler that answers
// it.
func ActionHandlerFor(notifType string) ActionHandler {
	id, _, ok := strings.Cut(notifType, ".")
	if !ok {
		return nil
	}
	a := ActorFor(id)
	if a == nil {
		return nil
	}
	route, found := a.ActionRoutes()[notifType]
	if !found {
		return nil
	}
	return route.Handler
}

// NotificationType is one entry of the catalogue served to clients.
//
// Clients used to hardcode this list, four times each, and adding a provider
// meant editing every copy — with no error when one was missed. Served, it is
// edited nowhere.
type NotificationType struct {
	Type         string `json:"type"`
	Provider     string `json:"provider"`
	Label        string `json:"label"`
	Detail       string `json:"detail,omitempty"`
	Blocking     bool   `json:"blocking"`
	Group        string `json:"group"`
	DefaultAlert bool   `json:"default_alert"`
}

// NotificationTypes returns every answerable notification type, sorted so the
// order a client renders is stable across restarts.
func NotificationTypes() []NotificationType {
	out := []NotificationType{}
	for _, p := range All() {
		id := p.Info().ID
		a := ActorFor(id)
		if a == nil {
			continue
		}
		for notifType, route := range a.ActionRoutes() {
			out = append(out, NotificationType{
				Type:         notifType,
				Provider:     id,
				Label:        route.Label,
				Detail:       route.Detail,
				Blocking:     route.Blocking,
				Group:        route.Group,
				DefaultAlert: route.DefaultAlert,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// Commands returns every provider's slash commands, keyed by provider ID.
func Commands() map[string][]Command {
	out := map[string][]Command{}
	for _, p := range All() {
		id := p.Info().ID
		if reg := lookup(id); reg != nil && reg.commander != nil {
			out[id] = reg.commander.Commands()
		}
	}
	return out
}

// EventTypes returns every provider's reportable events, keyed by provider ID.
func EventTypes() map[string][]EventTypeInfo {
	out := map[string][]EventTypeInfo{}
	for _, p := range All() {
		id := p.Info().ID
		if reg := lookup(id); reg != nil && reg.narrator != nil {
			out[id] = reg.narrator.EventTypes()
		}
	}
	return out
}

// DiscoverAll runs every provider's discovery scan.
func DiscoverAll(db *store.Store) {
	for _, p := range All() {
		if d := DiscovererFor(p.Info().ID); d != nil {
			d.Discover(db)
		}
	}
}
