package bot

import "sync"

// Stage tracks where a customer is in the order conversation.
type Stage int

const (
	StageBrowsing Stage = iota
	StageViewingFruit
	StageCart
	StageAwaitingAddress
	StageAwaitingPhone
	StageInvoice
	StageAwaitingReceipt
)

// CartItem is one fruit line in a customer's cart. Price is frozen at the
// moment the customer adds it, so a later admin price change never changes
// an order already in progress.
type CartItem struct {
	FruitID    string
	Emoji      string
	Name       string
	WeightKg   float64
	PricePerKg int
}

// LineTotal is this line's price in Toman.
func (c CartItem) LineTotal() int {
	return int(c.WeightKg * float64(c.PricePerKg))
}

// Session holds one customer's in-progress order.
type Session struct {
	Stage         Stage
	Cart          []CartItem
	CurrentFruit  string
	CurrentWeight float64
	Address       string
	Phone         string
}

// AddToCart merges the given weight into an existing line for the same
// fruit, or appends a new line.
func (s *Session) AddToCart(item CartItem) {
	for i := range s.Cart {
		if s.Cart[i].FruitID == item.FruitID {
			s.Cart[i].WeightKg += item.WeightKg
			return
		}
	}
	s.Cart = append(s.Cart, item)
}

// Total returns the cart's total price in Toman.
func (s *Session) Total() int {
	total := 0
	for _, item := range s.Cart {
		total += item.LineTotal()
	}
	return total
}

// resetOrder clears the cart and customer-entered details, keeping the browsing stage.
func (s *Session) resetOrder() {
	s.Stage = StageBrowsing
	s.Cart = nil
	s.CurrentFruit = ""
	s.CurrentWeight = 0
	s.Address = ""
	s.Phone = ""
}

// Store keeps one Session per chat, guarded by a mutex since updates are
// processed sequentially but from a single shared map.
type Store struct {
	mu       sync.Mutex
	sessions map[int64]*Session
}

// NewStore creates an empty session store.
func NewStore() *Store {
	return &Store{sessions: make(map[int64]*Session)}
}

// Get returns the session for chatID, creating one if needed.
func (s *Store) Get(chatID int64) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[chatID]
	if !ok {
		sess = &Session{Stage: StageBrowsing}
		s.sessions[chatID] = sess
	}
	return sess
}
