package ethereum

import (
	"encoding/hex"

	"github.com/umbracle/ethgo/wallet"
	"go.k6.io/k6/js/modules"
)

type Wallet struct{}

type Key struct {
	PrivateKey string
	Address    string
}

func init() {
	wallet := Wallet{}

	modules.Register("k6/x/ethereum/wallet", &wallet)
}

// GenerateKey creates a random key
func (w *Wallet) GenerateKey() (*Key, error) {
	k, err := wallet.GenerateKey()
	if err != nil {
		return nil, err
	}
	pk, err := k.MarshallPrivateKey()
	if err != nil {
		return nil, err
	}
	pks := hex.EncodeToString(pk)

	return &Key{
		PrivateKey: pks,
		Address:    k.Address().String(),
	}, err
}

func (w *Wallet) NewWalletKeyFromPrivateKey(privateKey []byte) (*Key, error) {
	wa, err := wallet.NewWalletFromPrivKey(privateKey)
	if err != nil {
		return nil, err
	}

	pk, err := wa.MarshallPrivateKey()
	if err != nil {
		return nil, err
	}
	pks := hex.EncodeToString(pk)

	return &Key{
		PrivateKey: pks,
		Address:    wa.Address().String(),
	}, err
}