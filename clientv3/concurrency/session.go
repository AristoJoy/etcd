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

package concurrency

import (
	"time"

	v3 "etcd/clientv3"
	"golang.org/x/net/context"
)

const defaultSessionTTL = 60

// Session represents a lease kept alive for the lifetime of a client.
// Fault-tolerant applications may use sessions to reason about liveness.
type Session struct {
	client *v3.Client
	opts   *sessionOptions
	id     v3.LeaseID

	cancel context.CancelFunc
	donec  <-chan struct{}
}

// NewSession gets the leased session for a client.
func NewSession(client *v3.Client, opts ...SessionOption) (*Session, error) {
	ops := &sessionOptions{ttl: defaultSessionTTL, ctx: client.Ctx()}
	for _, opt := range opts {
		opt(ops)
	}

	resp, err := client.Grant(ops.ctx, int64(ops.ttl))
	if err != nil {
		return nil, err
	}
	id := v3.LeaseID(resp.ID)

	ctx, cancel := context.WithCancel(ops.ctx)
	keepAlive, err := client.KeepAlive(ctx, id)
	if err != nil || keepAlive == nil {
		return nil, err
	}

	donec := make(chan struct{})
	s := &Session{client: client, opts: ops, id: id, cancel: cancel, donec: donec}

	// keep the lease alive until client error or cancelled context
	go func() {
		defer close(donec)
		for range keepAlive {
			// eat messages until keep alive channel closes
		}
	}()

	return s, nil
}

// Client is the etcd client that is attached to the session.
func (s *Session) Client() *v3.Client {
	return s.client
}

// Lease is the lease ID for keys bound to the session.
func (s *Session) Lease() v3.LeaseID { return s.id }

// Done returns a channel that closes when the lease is orphaned, expires, or
// is otherwise no longer being refreshed.
func (s *Session) Done() <-chan struct{} { return s.donec }

// Orphan ends the refresh for the session lease. This is useful
// in case the state of the client connection is indeterminate (revoke
// would fail) or when transferring lease ownership.
func (s *Session) Orphan() {
	s.cancel()
	<-s.donec
}

// Close orphans the session and revokes the session lease.
func (s *Session) Close() error {
	s.Orphan()
	// if revoke takes longer than the ttl, lease is expired anyway
	ctx, cancel := context.WithTimeout(s.opts.ctx, time.Duration(s.opts.ttl)*time.Second)
	_, err := s.client.Revoke(ctx, s.id)
	cancel()
	return err
}

type sessionOptions struct {
	ttl int
	ctx context.Context
}

// SessionOption configures Session.
type SessionOption func(*sessionOptions)

// WithTTL configures the session's TTL in seconds.
// If TTL is <= 0, the default 60 seconds TTL will be used.
func WithTTL(ttl int) SessionOption {
	return func(so *sessionOptions) {
		if ttl > 0 {
			so.ttl = ttl
		}
	}
}

// WithContext assigns a context to the session instead of defaulting to
// using the client context. This is useful for canceling NewSession and
// Close operations immediately without having to close the client. If the
// context is canceled before Close() completes, the session's lease will be
// abandoned and left to expire instead of being revoked.
func WithContext(ctx context.Context) SessionOption {
	return func(so *sessionOptions) {
		so.ctx = ctx
	}
}
