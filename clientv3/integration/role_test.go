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

func TestRoleError(t *testing.T) {
	defer testutil.AfterTest(t)

	clus := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	authapi := clientv3.NewAuth(clus.RandClient())

	_, err := authapi.RoleAdd(context.TODO(), "test-role")
	if err != nil {
		t.Fatal(err)
	}

	_, err = authapi.RoleAdd(context.TODO(), "test-role")
	if err != rpctypes.ErrRoleAlreadyExist {
		t.Fatalf("expected %v, got %v", rpctypes.ErrRoleAlreadyExist, err)
	}
}
