package controllers

import (
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type internalNameCache struct {
	generateNameToNamespaceName map[string]string
	mu                          sync.RWMutex
}

func (i *internalNameCache) Get(namespaceName string) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.generateNameToNamespaceName[namespaceName]
}

func (i *internalNameCache) Add(namespaceName string, generateName string) {
	n := i.Get(namespaceName)
	if n == generateName {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.generateNameToNamespaceName[namespaceName] = generateName
}

const (
	nameAnnotationKey      = "oadp.openshift.io/name-key"
	namespaceAnnotationKey = "oadp.openshift.io/namespace-key"
)

var (
	errAnnotationNotFound = fmt.Errorf("unable to get annotation information")
	nameCache             = internalNameCache{
		generateNameToNamespaceName: map[string]string{},
		mu:                          sync.RWMutex{},
	}
)

func addKeyToAnnotations(ann map[string]string, namespaceName client.ObjectKey) map[string]string {
	if ann == nil {
		ann = map[string]string{}
	}
	ann[nameAnnotationKey] = namespaceName.Name
	ann[namespaceAnnotationKey] = namespaceName.Namespace
	return ann
}

func getKeyFromAnnotations(annotations map[string]string) (client.ObjectKey, error) {
	var namespace string
	var name string
	if annotations == nil {
		return client.ObjectKey{}, errAnnotationNotFound
	}

	namespace, ok := annotations[namespaceAnnotationKey]
	if !ok {
		return client.ObjectKey{}, errAnnotationNotFound
	}

	name, ok = annotations[nameAnnotationKey]
	if !ok {
		return client.ObjectKey{}, errAnnotationNotFound
	}

	return client.ObjectKey{Namespace: namespace, Name: name}, nil
}
