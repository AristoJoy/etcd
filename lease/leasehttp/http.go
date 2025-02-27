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

package leasehttp

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	pb "etcd/etcdserver/etcdserverpb"
	"etcd/lease"
	"etcd/lease/leasepb"
	"etcd/pkg/httputil"
	"golang.org/x/net/context"
)

var (
	LeasePrefix         = "/leases"
	LeaseInternalPrefix = "/leases/internal"
	applyTimeout        = time.Second
	ErrLeaseHTTPTimeout = errors.New("waiting for node to catch up its applied index has timed out")
)

// NewHandler returns an http Handler for lease renewals
func NewHandler(l lease.Lessor, waitch func() <-chan struct{}) http.Handler {
	return &leaseHandler{l, waitch}
}

type leaseHandler struct {
	l      lease.Lessor
	waitch func() <-chan struct{}
}

func (h *leaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "error reading body", http.StatusBadRequest)
		return
	}

	var v []byte
	switch r.URL.Path {
	case LeasePrefix:
		lreq := pb.LeaseKeepAliveRequest{}
		if err := lreq.Unmarshal(b); err != nil {
			http.Error(w, "error unmarshalling request", http.StatusBadRequest)
			return
		}
		select {
		case <-h.waitch():
		case <-time.After(applyTimeout):
			http.Error(w, ErrLeaseHTTPTimeout.Error(), http.StatusRequestTimeout)
			return
		}
		ttl, err := h.l.Renew(lease.LeaseID(lreq.ID))
		if err != nil {
			if err == lease.ErrLeaseNotFound {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}

			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// TODO: fill out ResponseHeader
		resp := &pb.LeaseKeepAliveResponse{ID: lreq.ID, TTL: ttl}
		v, err = resp.Marshal()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	case LeaseInternalPrefix:
		lreq := leasepb.LeaseInternalRequest{}
		if err := lreq.Unmarshal(b); err != nil {
			http.Error(w, "error unmarshalling request", http.StatusBadRequest)
			return
		}
		select {
		case <-h.waitch():
		case <-time.After(applyTimeout):
			http.Error(w, ErrLeaseHTTPTimeout.Error(), http.StatusRequestTimeout)
			return
		}
		l := h.l.Lookup(lease.LeaseID(lreq.LeaseTimeToLiveRequest.ID))
		if l == nil {
			http.Error(w, lease.ErrLeaseNotFound.Error(), http.StatusNotFound)
			return
		}
		// TODO: fill out ResponseHeader
		resp := &leasepb.LeaseInternalResponse{
			LeaseTimeToLiveResponse: &pb.LeaseTimeToLiveResponse{
				Header:     &pb.ResponseHeader{},
				ID:         lreq.LeaseTimeToLiveRequest.ID,
				TTL:        int64(l.Remaining().Seconds()),
				GrantedTTL: l.TTL(),
			},
		}
		if lreq.LeaseTimeToLiveRequest.Keys {
			ks := l.Keys()
			kbs := make([][]byte, len(ks))
			for i := range ks {
				kbs[i] = []byte(ks[i])
			}
			resp.LeaseTimeToLiveResponse.Keys = kbs
		}

		v, err = resp.Marshal()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, fmt.Sprintf("unknown request path %q", r.URL.Path), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/protobuf")
	w.Write(v)
}

// RenewHTTP renews a lease at a given primary server.
// TODO: Batch request in future?
func RenewHTTP(ctx context.Context, id lease.LeaseID, url string, rt http.RoundTripper) (int64, error) {
	// will post lreq protobuf to leader
	lreq, err := (&pb.LeaseKeepAliveRequest{ID: int64(id)}).Marshal()
	if err != nil {
		return -1, err
	}

	cc := &http.Client{Transport: rt}
	req, err := http.NewRequest("POST", url, bytes.NewReader(lreq))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", "application/protobuf")
	req.Cancel = ctx.Done()

	resp, err := cc.Do(req)
	if err != nil {
		return -1, err
	}
	b, err := readResponse(resp)
	if err != nil {
		return -1, err
	}

	if resp.StatusCode == http.StatusRequestTimeout {
		return -1, ErrLeaseHTTPTimeout
	}

	if resp.StatusCode == http.StatusNotFound {
		return -1, lease.ErrLeaseNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("lease: unknown error(%s)", string(b))
	}

	lresp := &pb.LeaseKeepAliveResponse{}
	if err := lresp.Unmarshal(b); err != nil {
		return -1, fmt.Errorf(`lease: %v. data = "%s"`, err, string(b))
	}
	if lresp.ID != int64(id) {
		return -1, fmt.Errorf("lease: renew id mismatch")
	}
	return lresp.TTL, nil
}

// TimeToLiveHTTP retrieves lease information of the given lease ID.
func TimeToLiveHTTP(ctx context.Context, id lease.LeaseID, keys bool, url string, rt http.RoundTripper) (*leasepb.LeaseInternalResponse, error) {
	// will post lreq protobuf to leader
	lreq, err := (&leasepb.LeaseInternalRequest{&pb.LeaseTimeToLiveRequest{ID: int64(id), Keys: keys}}).Marshal()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(lreq))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/protobuf")

	cancel := httputil.RequestCanceler(req)

	cc := &http.Client{Transport: rt}
	var b []byte
	// buffer errc channel so that errc don't block inside the go routinue
	errc := make(chan error, 2)
	go func() {
		resp, err := cc.Do(req)
		if err != nil {
			errc <- err
			return
		}
		b, err = readResponse(resp)
		if err != nil {
			errc <- err
			return
		}
		if resp.StatusCode == http.StatusRequestTimeout {
			errc <- ErrLeaseHTTPTimeout
			return
		}
		if resp.StatusCode == http.StatusNotFound {
			errc <- lease.ErrLeaseNotFound
			return
		}
		if resp.StatusCode != http.StatusOK {
			errc <- fmt.Errorf("lease: unknown error(%s)", string(b))
			return
		}
		errc <- nil
	}()
	select {
	case derr := <-errc:
		if derr != nil {
			return nil, derr
		}
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}

	lresp := &leasepb.LeaseInternalResponse{}
	if err := lresp.Unmarshal(b); err != nil {
		return nil, fmt.Errorf(`lease: %v. data = "%s"`, err, string(b))
	}
	if lresp.LeaseTimeToLiveResponse.ID != int64(id) {
		return nil, fmt.Errorf("lease: renew id mismatch")
	}
	return lresp, nil
}

func readResponse(resp *http.Response) (b []byte, err error) {
	b, err = ioutil.ReadAll(resp.Body)
	httputil.GracefulClose(resp)
	return
}
