/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
This controller assumes each fullnode has a secret that contains its wallets.
The wallets are stored redundantly in kubernetes:
  1. The Wallet CRD, which contains a list of fullnodes where the wallet should be installed.
	2. The fullnode wallet secret, which contains a map of all the wallets that have been installed there.
An alternative approach might simply use the lotus API to import wallets into the lotus daemon
and avoid storing them in the fullnode secrets.
*/

package controllers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/filecoin-project/go-address"
	lotustypes "github.com/filecoin-project/lotus/chain/types"
	lotuswallet "github.com/filecoin-project/lotus/chain/wallet"
	lotussigs "github.com/filecoin-project/lotus/lib/sigs"
	_ "github.com/filecoin-project/lotus/lib/sigs/bls"
	_ "github.com/filecoin-project/lotus/lib/sigs/secp"

	filecoiniov1alpha1 "github.com/coryschwartz/lotus-kubeapi/api/v1alpha1"
)

// WalletReconciler reconciles a Wallet object
type WalletReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=filecoin.io.filecoin.io,resources=wallets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=filecoin.io.filecoin.io,resources=wallets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;update;patch

func (r *WalletReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("wallet", req.NamespacedName)
	log.Info("processing wallet")

	var wallet filecoiniov1alpha1.Wallet
	if err := r.Get(ctx, req.NamespacedName, &wallet); err != nil {
		log.Error(err, "unable to fetch wallet:")
		return ctrl.Result{}, err
	}

	// If the wallet is fully deployed, don't continue, there's nothing to do.
	if WalletIsFullyDeployed(&wallet) {
		log.Info("wallet is already fully deployed.")
		return ctrl.Result{}, nil
	}

	// If the wallet was generated outside of the system, keep it as it is.
	// but if we have a Wallet resource without any lotus wallet content,
	// lets generate a random one.
	if err := ValidateOrGenerateWallet(&wallet); err != nil {
		log.Error(err, "could not generate new wallet.")
		return ctrl.Result{}, err
	}

	// Every fullnode has a secret that contains all of its wallets.
	// For every fullnode, add this wallet to its secret.
	for _, fullnode := range wallet.Spec.Fullnodes {
		sec := new(corev1.Secret)
		key := client.ObjectKey{
			Namespace: req.Namespace,
			Name:      fmt.Sprintf("%s-wallets", fullnode),
		}
		if err := r.Get(ctx, key, sec); err != nil {
			log.Error(err, fmt.Sprintf("could not get wallet secret for %s", fullnode))
			return ctrl.Result{}, err
		}
		if sec.StringData == nil {
			sec.StringData = make(map[string]string, 0)
		}
		sec.StringData[wallet.Spec.Address] = wallet.Spec.Exported
		if err := r.Update(ctx, sec); err != nil {
			return ctrl.Result{}, err
		}
		wallet.Status.DeployedFullnodes = append(wallet.Status.DeployedFullnodes, fullnode)
		if err := r.Update(ctx, &wallet); err != nil {
			log.Error(err, "could not update wallet CRD.")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&filecoiniov1alpha1.Wallet{}).
		Complete(r)
}

// Check if the wallet is fully deployed
func WalletIsFullyDeployed(wallet *filecoiniov1alpha1.Wallet) bool {
	for _, n := range wallet.Spec.Fullnodes {
		found := false
		for _, d := range wallet.Status.DeployedFullnodes {
			if n == d {
				found = true
				continue
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Decide if wallet is completely filled out.
// If it isn't filled out correctly, fill it out.
// If there is no exported, then the wallet will be generated.
// If there is an exported but no address, fill out the address.
func ValidateOrGenerateWallet(wallet *filecoiniov1alpha1.Wallet) error {
	if wallet.Spec.Exported == "" {
		return GenerateWallet(wallet)
	}
	if wallet.Spec.Address == "" {
		ki, err := KeyInfoFromWallet(wallet)
		if err != nil {
			return err
		}
		pub, err := lotussigs.ToPublic(lotuswallet.ActSigType(ki.Type), ki.PrivateKey)
		if err != nil {
			return err
		}
		addr, err := address.NewBLSAddress(pub)
		if err != nil {
			return err
		}
		wallet.Spec.Address = addr.String()
		return nil
	}
	return nil
}

// Fills out wallet fields
func GenerateWallet(wallet *filecoiniov1alpha1.Wallet) error {
	if wallet.Spec.KeyType == "" {
		wallet.Spec.KeyType = "bls"
	}
	kt := lotustypes.KeyType(wallet.Spec.KeyType)
	switch kt {
	case lotustypes.KTSecp256k1, lotustypes.KTBLS:
		key, err := lotuswallet.GenerateKey(kt)
		if err != nil {
			return err
		}
		wallet.Spec.Address = key.Address.String()
		kb, err := json.Marshal(key.KeyInfo)
		if err != nil {
			return err
		}
		wallet.Spec.Exported = hex.EncodeToString(kb)
	default:
		return errors.New("unsupported KeyType")
	}
	return nil
}

func KeyInfoFromWallet(wallet *filecoiniov1alpha1.Wallet) (ki *lotustypes.KeyInfo, err error) {
	ki = new(lotustypes.KeyInfo)
	kb, err := hex.DecodeString(wallet.Spec.Exported)
	if err != nil {
		return ki, err
	}
	err = json.Unmarshal(kb, &ki)
	return ki, err
}
