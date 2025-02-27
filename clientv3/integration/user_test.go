// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"testing"

	"etcd/clientv3"
	"etcd/etcdserver/api/v3rpc/rpctypes"
	"etcd/integration"
	"etcd/pkg/testutil"
	"golang.org/x/net/context"
)

func TestUserError(t *testing.T) {
	defer testutil.AfterTest(t)

	clus := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	authapi := clientv3.NewAuth(clus.RandClient())

	_, err := authapi.UserAdd(context.TODO(), "foo", "bar")
	if err != nil {
		t.Fatal(err)
	}

	_, err = authapi.UserAdd(context.TODO(), "foo", "bar")
	if err != rpctypes.ErrUserAlreadyExist {
		t.Fatalf("expected %v, got %v", rpctypes.ErrUserAlreadyExist, err)
	}

	_, err = authapi.UserDelete(context.TODO(), "not-exist-user")
	if err != rpctypes.ErrUserNotFound {
		t.Fatalf("expected %v, got %v", rpctypes.ErrUserNotFound, err)
	}

	_, err = authapi.UserGrantRole(context.TODO(), "foo", "test-role-does-not-exist")
	if err != rpctypes.ErrRoleNotFound {
		t.Fatalf("expected %v, got %v", rpctypes.ErrRoleNotFound, err)
	}
}
