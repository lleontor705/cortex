//go:build postgres_integration

package postgres

// These unexported seams exist only for the package's PostgreSQL migration
// tests. Production callers use AuthorizedStore operations exclusively.
func (s *Store) sessions() *SessionRepository                   { return &SessionRepository{s} }
func (s *Store) entities() *EntityRepository                    { return &EntityRepository{s} }
func (s *Store) outbox() *OutboxStore                           { return &OutboxStore{s} }
func (s *AuthorizedStore) observations() *ObservationRepository { return s.store.observations() }
func (s *AuthorizedStore) sessions() *SessionRepository         { return s.store.sessions() }
func (s *AuthorizedStore) prompts() *PromptRepository           { return s.store.prompts() }
func (s *AuthorizedStore) graph() *GraphRepository              { return s.store.graph() }
func (s *AuthorizedStore) entities() *EntityRepository          { return s.store.entities() }
func (s *AuthorizedStore) search() *SearchRepository            { return s.store.search() }
func (s *AuthorizedStore) outbox() *OutboxStore                 { return s.store.outbox() }
func (s *AuthorizedStore) tokens() *TokenRepository             { return s.store.tokens() }
