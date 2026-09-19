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

// CartItem is one fruit line in a customer's cart.
type CartItem struct {
	FruitID  string
	WeightKg float64
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

// AddToCart merges the given weight into an existing line for the same fruit,
// or appends a new line.
func (s *Session) AddToCart(fruitID string, weightKg float64) {
	for i := range s.Cart {
		if s.Cart[i].FruitID == fruitID {
			s.Cart[i].WeightKg += weightKg
			return
		}
	}
	s.Cart = append(s.Cart, CartItem{FruitID: fruitID, WeightKg: weightKg})
}

// Total returns the cart's total price in Toman.
func (s *Session) Total() int {
	total := 0
	for _, item := range s.Cart {
		if fruit := FindFruit(item.FruitID); fruit != nil {
			total += int(item.WeightKg * float64(fruit.Price))
		}
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
