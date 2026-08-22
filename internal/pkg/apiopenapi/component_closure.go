package apiopenapi

import (
	"encoding/json"
	"fmt"
)

type componentIdentity struct {
	collection string
	name       string
	path       string
}

type componentGraph struct {
	roots    []componentIdentity
	outgoing map[componentIdentity][]componentIdentity
}

type componentGraphStats struct {
	componentEntries     int
	referenceResolutions int
	ownerLookups         int
	ownerPathSteps       int
	edgeInsertions       int
}

type componentOwnerNode struct {
	children map[byte]*componentOwnerNode
	owner    *componentIdentity
}

type componentOwnerIndex struct {
	root componentOwnerNode
}

type componentGraphBuilder struct {
	validator  *referenceValidator
	identities map[componentIdentity]componentIdentity
	owners     componentOwnerIndex
	graph      componentGraph
	stats      componentGraphStats
}

// RetainReachableComponents removes reusable components that are not reachable
// from the exported paths and document-level semantic references. The input
// must be an export already validated by ParseJSON, BuildDocument, or
// BuildExport; this function performs closure pruning, not full validation.
func RetainReachableComponents(raw []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("decode exported OpenAPI document: %w", err)
	}
	components, _ := root["components"].(map[string]any)
	if components == nil {
		return raw, nil
	}

	validator := newReferenceValidator(root)
	validator.collectDocument(root)
	if validator.problem != nil {
		return nil, fmt.Errorf("collect exported OpenAPI references: %s at %s", validator.problem.Code, validator.problem.Path)
	}
	identities := componentIdentities(components)
	graph, _, err := buildComponentGraph(validator, identities)
	if err != nil {
		return nil, err
	}
	reachable := make(map[componentIdentity]struct{})
	queue := make([]componentIdentity, 0, len(identities))
	add := func(identity componentIdentity) {
		if _, seen := reachable[identity]; seen {
			return
		}
		reachable[identity] = struct{}{}
		queue = append(queue, identity)
	}
	for _, root := range graph.roots {
		add(root)
	}
	for index := 0; index < len(queue); index++ {
		for _, target := range graph.outgoing[queue[index]] {
			add(target)
		}
	}

	retained := make(map[string]any)
	for identity := range reachable {
		collection, _ := retained[identity.collection].(map[string]any)
		if collection == nil {
			collection = make(map[string]any)
			retained[identity.collection] = collection
		}
		items := components[identity.collection].(map[string]any)
		collection[identity.name] = items[identity.name]
	}
	if len(retained) == 0 {
		delete(root, "components")
	} else {
		root["components"] = retained
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode scoped OpenAPI document: %w", err)
	}
	return append(encoded, '\n'), nil
}

func buildComponentGraph(validator *referenceValidator, identities map[componentIdentity]componentIdentity) (componentGraph, componentGraphStats, error) {
	builder := componentGraphBuilder{
		validator: validator, identities: identities, owners: newComponentOwnerIndex(identities),
		graph: componentGraph{outgoing: make(map[componentIdentity][]componentIdentity)},
		stats: componentGraphStats{componentEntries: len(identities)},
	}
	for _, reference := range validator.references {
		if err := builder.addReference(reference); err != nil {
			return componentGraph{}, builder.stats, err
		}
	}
	for _, reference := range validator.componentRefs {
		if err := builder.addComponentReference(reference); err != nil {
			return componentGraph{}, builder.stats, err
		}
	}
	return builder.graph, builder.stats, nil
}

func (builder *componentGraphBuilder) addReference(reference referenceRecord) error {
	source, hasSource := builder.owners.lookup(reference.path, &builder.stats)
	target, hasTarget, err := builder.resolveTarget(reference)
	if err != nil {
		return err
	}
	builder.addEdge(source, hasSource, target, hasTarget)
	return nil
}

func (builder *componentGraphBuilder) addComponentReference(reference componentReferenceRecord) error {
	source, hasSource := builder.owners.lookup(reference.path, &builder.stats)
	if reference.localReference != nil {
		target, hasTarget, err := builder.resolveTarget(*reference.localReference)
		if err != nil {
			return err
		}
		builder.addEdge(source, hasSource, target, hasTarget)
		return nil
	}
	target, hasTarget := builder.identities[componentIdentity{collection: reference.collection, name: reference.name}]
	builder.addEdge(source, hasSource, target, hasTarget)
	return nil
}

func (builder *componentGraphBuilder) resolveTarget(reference referenceRecord) (componentIdentity, bool, error) {
	builder.stats.referenceResolutions++
	target, problem := builder.validator.resolve(reference)
	if problem != nil {
		return componentIdentity{}, false, fmt.Errorf("resolve exported OpenAPI reference: %s at %s", problem.Code, problem.Path)
	}
	owner, exists := builder.owners.lookup(target.path, &builder.stats)
	return owner, exists, nil
}

func (builder *componentGraphBuilder) addEdge(source componentIdentity, hasSource bool, target componentIdentity, hasTarget bool) {
	if !hasTarget {
		return
	}
	builder.stats.edgeInsertions++
	if hasSource {
		builder.graph.outgoing[source] = append(builder.graph.outgoing[source], target)
	} else {
		builder.graph.roots = append(builder.graph.roots, target)
	}
}

func newComponentOwnerIndex(identities map[componentIdentity]componentIdentity) componentOwnerIndex {
	index := componentOwnerIndex{root: componentOwnerNode{children: make(map[byte]*componentOwnerNode)}}
	for _, identity := range identities {
		node := &index.root
		for position := 0; position < len(identity.path); position++ {
			if node.children == nil {
				node.children = make(map[byte]*componentOwnerNode)
			}
			child := node.children[identity.path[position]]
			if child == nil {
				child = &componentOwnerNode{}
				node.children[identity.path[position]] = child
			}
			node = child
		}
		owner := identity
		node.owner = &owner
	}
	return index
}

func (index *componentOwnerIndex) lookup(path string, stats *componentGraphStats) (componentIdentity, bool) {
	stats.ownerLookups++
	node := &index.root
	for position := 0; position < len(path); position++ {
		stats.ownerPathSteps++
		node = node.children[path[position]]
		if node == nil {
			return componentIdentity{}, false
		}
		if node.owner != nil && (position+1 == len(path) || path[position+1] == '.' || path[position+1] == '[') {
			return *node.owner, true
		}
	}
	return componentIdentity{}, false
}

func componentIdentities(components map[string]any) map[componentIdentity]componentIdentity {
	identities := make(map[componentIdentity]componentIdentity)
	for _, collection := range sortedAnyKeys(components) {
		items, _ := components[collection].(map[string]any)
		for _, name := range sortedAnyKeys(items) {
			identity := componentIdentity{
				collection: collection,
				name:       name,
				path:       appendJSONPath(appendJSONPath("$.components", collection), name),
			}
			identities[componentIdentity{collection: collection, name: name}] = identity
		}
	}
	return identities
}
