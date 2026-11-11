package wallet

type ChainKind string

const (
	ChainKindEVM    ChainKind = "evm"
	ChainKindSolana ChainKind = "solana"
)

type Wallet struct {
	Address    string
	Label      string
	ChainKind  ChainKind
	privateKey []byte
}

type PublicWallet struct {
	Address   string
	Label     string
	ChainKind ChainKind
}

type WalletSet struct {
	items    []*Wallet
	byLabel  map[string]*Wallet
	byAddr   map[string]*Wallet
	failures []UnlockFailure
}

type UnlockFailure struct {
	File  string
	Label string
	Err   error
}

func New(address string, label string, kind ChainKind, privateKey []byte) *Wallet {
	key := make([]byte, len(privateKey))
	copy(key, privateKey)
	return &Wallet{
		Address:    address,
		Label:      label,
		ChainKind:  kind,
		privateKey: key,
	}
}

func NewSet(items []*Wallet, failures []UnlockFailure) *WalletSet {
	set := &WalletSet{
		items:    make([]*Wallet, 0, len(items)),
		byLabel:  make(map[string]*Wallet, len(items)),
		byAddr:   make(map[string]*Wallet, len(items)),
		failures: append([]UnlockFailure(nil), failures...),
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		set.items = append(set.items, item)
		set.byLabel[item.Label] = item
		set.byAddr[item.Address] = item
	}
	return set
}

func (w *Wallet) PrivateKeyCopy() []byte {
	if w == nil {
		return nil
	}
	key := make([]byte, len(w.privateKey))
	copy(key, w.privateKey)
	return key
}

func (w *Wallet) Public() PublicWallet {
	if w == nil {
		return PublicWallet{}
	}
	return PublicWallet{
		Address:   w.Address,
		Label:     w.Label,
		ChainKind: w.ChainKind,
	}
}

func (w *Wallet) Zeroize() {
	if w == nil {
		return
	}
	for i := range w.privateKey {
		w.privateKey[i] = 0
	}
}

func (s *WalletSet) Count() int {
	if s == nil {
		return 0
	}
	return len(s.items)
}

func (s *WalletSet) Items() []PublicWallet {
	if s == nil {
		return nil
	}
	items := make([]PublicWallet, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item.Public())
	}
	return items
}

func (s *WalletSet) Failures() []UnlockFailure {
	if s == nil {
		return nil
	}
	return append([]UnlockFailure(nil), s.failures...)
}

// ByLabel returns the unlocked wallet matching label.
func (s *WalletSet) ByLabel(label string) *Wallet {
	if s == nil {
		return nil
	}
	return s.byLabel[label]
}

func (s *WalletSet) Zeroize() {
	if s == nil {
		return
	}
	for _, item := range s.items {
		item.Zeroize()
	}
}
