package model_routing

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const RoutingWalkMaxDepth = 5

type RoutingWalkNodeKind string

const (
	RoutingWalkNodeModel   RoutingWalkNodeKind = "model"
	RoutingWalkNodeRouting RoutingWalkNodeKind = "routing"
	RoutingWalkNodeInvalid RoutingWalkNodeKind = "invalid"
)

type RoutingWalkIssue string

const (
	RoutingWalkIssueCycle    RoutingWalkIssue = "cycle"
	RoutingWalkIssueMaxDepth RoutingWalkIssue = "max_depth"
	RoutingWalkIssueDisabled RoutingWalkIssue = "disabled"
	RoutingWalkIssueNotFound RoutingWalkIssue = "not_found"
)

// RoutingWalkNode is the shared internal traversal result. It deliberately
// retains administrator diagnostics; ordinary marketplace responses must map
// it into their own allowlist DTO instead of serializing this type.
type RoutingWalkNode struct {
	Ref              string
	Kind             RoutingWalkNodeKind
	RouteKey         string
	Scope            string
	Priority         int
	Weight           int
	EffectivePercent float64
	Children         []RoutingWalkNode
	Issue            RoutingWalkIssue
}

// RoutingWalkRoute is the minimum routing definition needed by the pure walk.
// Key identifies the concrete route row, so a user route and global route with
// the same name do not become a false cycle.
type RoutingWalkRoute struct {
	Key     string
	Scope   string
	Members []models.RoutingMember
}

// RoutingTargetFinder is the strategy boundary between pure traversal and the
// caller's indexes/parsing policy. Marketplace uses strict JSON parsing;
// administrator preview keeps its existing best-effort behavior.
type RoutingTargetFinder interface {
	FindEnabledRouting(ref string) (RoutingWalkRoute, bool, error)
	FindDisabledRouting(ref string) (RoutingWalkRoute, bool, error)
	HasRealModel(ref string) bool
}

type RoutingWalkRequest struct {
	RootRouteKey string
	Members      []models.RoutingMember
}

// RoutingWalkKey returns a stable path identity for a concrete routing row.
func RoutingWalkKey(r *models.ModelRouting) string {
	if r == nil {
		return ""
	}
	if r.ID != 0 {
		return fmt.Sprintf("id:%d", r.ID)
	}
	return fmt.Sprintf("scope:%s:user:%d:token:%d:name:%s", r.Scope, r.UserID, r.TokenID, r.Name)
}

// WalkRoutingDestinations traverses all priority groups deterministically. The
// effective percentage is retained solely for the administrator preview; the
// marketplace visitor ignores it and exposes every possible terminal offer.
func WalkRoutingDestinations(
	request RoutingWalkRequest,
	targets RoutingTargetFinder,
) ([]RoutingWalkNode, error) {
	visited := make(map[string]bool)
	if request.RootRouteKey != "" {
		visited[request.RootRouteKey] = true
	}
	return walkRoutingMembers(request.Members, targets, visited, 1, 100)
}

func walkRoutingMembers(
	members []models.RoutingMember,
	targets RoutingTargetFinder,
	visited map[string]bool,
	depth int,
	parentPercent float64,
) ([]RoutingWalkNode, error) {
	if len(members) == 0 {
		return nil, nil
	}

	ordered, topPriority, topWeight := routingWalkOrder(members)
	children := make([]RoutingWalkNode, 0, len(ordered))
	for _, member := range ordered {
		effectivePercent := 0.0
		if member.Priority == topPriority {
			effectivePercent = parentPercent * float64(effectiveRoutingWeight(member.Weight)) / float64(topWeight)
		}
		// behavior change: a max-depth terminal keeps the configured member's
		// identity while remaining invalid and unexpanded.
		if depth > RoutingWalkMaxDepth {
			children = append(children, RoutingWalkNode{
				Ref: member.Ref, Kind: RoutingWalkNodeInvalid,
				Priority: member.Priority, Weight: member.Weight, EffectivePercent: effectivePercent,
				Issue: RoutingWalkIssueMaxDepth,
			})
			continue
		}
		node, err := walkRoutingMember(member, targets, visited, depth, effectivePercent)
		if err != nil {
			return nil, err
		}
		children = append(children, node)
	}
	return children, nil
}

func walkRoutingMember(
	member models.RoutingMember,
	targets RoutingTargetFinder,
	visited map[string]bool,
	depth int,
	effectivePercent float64,
) (RoutingWalkNode, error) {
	node := RoutingWalkNode{
		Ref: member.Ref, Priority: member.Priority, Weight: member.Weight, EffectivePercent: effectivePercent,
	}
	route, found, err := targets.FindEnabledRouting(member.Ref)
	if err != nil {
		return RoutingWalkNode{}, err
	}
	if found {
		return walkEnabledRouting(node, route, targets, visited, depth)
	}
	disabled, found, err := targets.FindDisabledRouting(member.Ref)
	if err != nil {
		return RoutingWalkNode{}, err
	}
	if found {
		node.Kind = RoutingWalkNodeRouting
		node.RouteKey = disabled.Key
		node.Scope = disabled.Scope
		node.Issue = RoutingWalkIssueDisabled
		return node, nil
	}
	if targets.HasRealModel(member.Ref) {
		node.Kind = RoutingWalkNodeModel
		return node, nil
	}
	node.Kind = RoutingWalkNodeInvalid
	node.Issue = RoutingWalkIssueNotFound
	return node, nil
}

func walkEnabledRouting(
	node RoutingWalkNode,
	route RoutingWalkRoute,
	targets RoutingTargetFinder,
	visited map[string]bool,
	depth int,
) (RoutingWalkNode, error) {
	if visited[route.Key] {
		if targets.HasRealModel(node.Ref) {
			node.Kind = RoutingWalkNodeModel
			return node, nil
		}
		node.Kind = RoutingWalkNodeRouting
		node.RouteKey = route.Key
		node.Issue = RoutingWalkIssueCycle
		return node, nil
	}

	node.Kind = RoutingWalkNodeRouting
	node.RouteKey = route.Key
	node.Scope = route.Scope
	visited[route.Key] = true
	children, err := walkRoutingMembers(route.Members, targets, visited, depth+1, node.EffectivePercent)
	delete(visited, route.Key)
	if err != nil {
		return RoutingWalkNode{}, err
	}
	node.Children = children
	return node, nil
}

func routingWalkOrder(members []models.RoutingMember) ([]models.RoutingMember, int, int) {
	topPriority := members[0].Priority
	for _, member := range members[1:] {
		if member.Priority > topPriority {
			topPriority = member.Priority
		}
	}
	ordered := make([]models.RoutingMember, 0, len(members))
	topWeight := 0
	for _, member := range members {
		if member.Priority == topPriority {
			ordered = append(ordered, member)
			topWeight += effectiveRoutingWeight(member.Weight)
		}
	}
	for _, member := range members {
		if member.Priority != topPriority {
			ordered = append(ordered, member)
		}
	}
	return ordered, topPriority, topWeight
}

func effectiveRoutingWeight(weight int) int {
	if weight <= 0 {
		return 1
	}
	return weight
}
