// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package namespace

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/vmware/virtual-security-module/config"
	"github.com/vmware/virtual-security-module/model"
	"github.com/vmware/virtual-security-module/vds"
	"github.com/vmware/virtual-security-module/vks"
)

var nm *NamespaceManager

func TestMain(m *testing.M) {
	cfg := config.GenerateTestConfig()

	ds, err := vds.GetDataStoreFromConfig(cfg)
	if err != nil {
		fmt.Printf("Failed to get data store from config: %v\n", err)
		os.Exit(1)
	}

	ks, err := vks.GetKeyStoreFromConfig(cfg)
	if err != nil {
		fmt.Printf("Failed to get key store from config: %v\n", err)
		os.Exit(1)
	}

	nm = New()
	if err := nm.Init(cfg, ds, ks); err != nil {
		fmt.Printf("Failed to initialize namespace manager: %v\n", err)
		os.Exit(1)
	}
	defer nm.Close()

	apiTestSetup()
	defer apiTestCleanup()

	os.Exit(m.Run())
}

func TestCreateAndGetNamespace(t *testing.T) {
	ne := &model.NamespaceEntry{
		Path:                   "/",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	id, err := nm.CreateNamespace(ne)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	if len(id) == 0 {
		t.Fatalf("Failed to create namespace: returned id is empty")
	}

	ne2, err := nm.GetNamespace(id)
	if err != nil {
		t.Fatalf("Failed to get namespace for id %v: %v", id, err)
	}

	ne2.ChildPaths = ne.ChildPaths
	if !reflect.DeepEqual(ne, ne2) {
		t.Fatalf("Created and retrieved namespaces are different: %v %v", ne, ne2)
	}

	if err := nm.DeleteNamespace(id); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	ne := &model.NamespaceEntry{
		Path:                   "/",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	id, err := nm.CreateNamespace(ne)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	if _, err := nm.CreateNamespace(ne); err == nil {
		t.Fatal("Succeeded to create the same namespace twice")
	}

	child := &model.NamespaceEntry{
		Path:                   "/child",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	childId, err := nm.CreateNamespace(child)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	if _, err := nm.CreateNamespace(child); err == nil {
		t.Fatal("Succeeded to create the same namespace twice")
	}

	if err := nm.DeleteNamespace(childId); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}

	if err := nm.DeleteNamespace(id); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}
}

func TestCreateParentNotExists(t *testing.T) {
	ne := &model.NamespaceEntry{
		Path:                   "/child",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	if _, err := nm.CreateNamespace(ne); err == nil {
		t.Fatal("Succeeded to create a child namespace of a non-existent parent")
	}
}

func TestGetNotExists(t *testing.T) {
	if _, err := nm.GetNamespace("/not/exists"); err == nil {
		t.Fatal("Succeeded to get a non-existent namespace")
	}
}

func TestDeleteParentNotEmpty(t *testing.T) {
	root := &model.NamespaceEntry{
		Path:                   "/",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	rootId, err := nm.CreateNamespace(root)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	child := &model.NamespaceEntry{
		Path:                   "/child",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	childId, err := nm.CreateNamespace(child)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	grandchild := &model.NamespaceEntry{
		Path:                   "/child/grandchild",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	grandchildId, err := nm.CreateNamespace(grandchild)
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	if err := nm.DeleteNamespace(rootId); err == nil {
		t.Fatal("Succeeded delete namespace that has children")
	}

	if err := nm.DeleteNamespace(childId); err == nil {
		t.Fatal("Succeeded delete namespace that has children")
	}

	if err := nm.DeleteNamespace(grandchildId); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}

	if err := nm.DeleteNamespace(childId); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}

	if err := nm.DeleteNamespace(rootId); err != nil {
		t.Fatalf("Faied to delete namespace:%v", err)
	}
}

func TestNamespaceNavigation(t *testing.T) {
	root := &model.NamespaceEntry{
		Path:                   "/",
		OwnerEntryId:           "user0",
		AuthorizationPolicyIds: []string{},
	}

	if _, err := nm.CreateNamespace(root); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	childCount := 3
	for i := 0; i < childCount; i++ {
		child := &model.NamespaceEntry{
			Path:                   fmt.Sprintf("/%v", i),
			OwnerEntryId:           fmt.Sprintf("user-%v", i),
			AuthorizationPolicyIds: []string{},
		}

		if _, err := nm.CreateNamespace(child); err != nil {
			t.Fatalf("Failed to create namespace: %v", err)
		}
	}

	root2, err := nm.GetNamespace("/")
	if err != nil {
		t.Fatalf("Failed to get namespace: %v", err)
	}

	if len(root2.ChildPaths) != childCount {
		t.Fatalf("Root namespace has different number of children %v than expected: %v", len(root2.ChildPaths), childCount)
	}

	for i := 0; i < childCount; i++ {
		path := fmt.Sprintf("/%v", i)
		expectedOwnerId := fmt.Sprintf("user-%v", i)

		child, err := nm.GetNamespace(path)
		if err != nil {
			t.Fatalf("Failed to get namespace: %v", err)
		}

		if expectedOwnerId != child.OwnerEntryId {
			t.Fatalf("Created and retrieved owner ids are different: %v, %v", expectedOwnerId, child.OwnerEntryId)
		}

		if err := nm.DeleteNamespace(path); err != nil {
			t.Fatalf("Failed to delete namespace: %v", err)
		}
	}
}