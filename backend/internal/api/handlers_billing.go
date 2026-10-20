package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mr-tron/base58"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// PurchaseTTL is how long a pending purchase waits for its transaction.
const PurchaseTTL = 2 * time.Hour

type createPurchaseIn struct {
	Body struct {
		SKU   core.SKU `json:"sku" enum:"mini,pro"`
		Promo string   `json:"promo,omitempty"`
	}
}

type confirmPurchaseIn struct {
	ID   string `path:"id"`
	Body struct {
		TxSignature string `json:"tx_signature"`
	}
}

type tweetClaimIn struct {
	Body struct {
		URL string `json:"url" doc:"Link to the post containing the user's nonce"`
	}
}

type tweetNonceOut struct {
	Body struct {
		Nonce string `json:"nonce"`
		Text  string `json:"text" doc:"Suggested post text"`
	}
}

func (d *Deps) registerBilling(api huma.API) {
	huma.Register(api, withAuth(op("createPurchase", http.MethodPost, "/v1/purchases", "Start a USDC purchase", "billing"), userAuth), d.createPurchase)
	huma.Register(api, withAuth(op("confirmPurchase", http.MethodPost, "/v1/purchases/{id}/confirm", "Submit the transaction signature", "billing"), userAuth), d.confirmPurchase)
	huma.Register(api, withAuth(op("getPurchase", http.MethodGet, "/v1/purchases/{id}", "Purchase", "billing"), userAuth), d.getPurchase)
	huma.Register(api, withAuth(op("tweetNonce", http.MethodGet, "/v1/tweet-claims/nonce", "My nonce and suggested post", "billing"), userAuth), d.tweetNonce)
	huma.Register(api, withAuth(op("createTweetClaim", http.MethodPost, "/v1/tweet-claims", "Submit a post link for a free audit", "billing"), userAuth), d.createTweetClaim)
	huma.Register(api, withAuth(op("listTweetClaims", http.MethodGet, "/v1/tweet-claims", "My claims", "billing"), userAuth), d.listTweetClaims)
}

func (d *Deps) treasuryReady() error {
	if d.Config.TreasuryPubkey == "" {
		return huma.Error503ServiceUnavailable("USDC payments not configured")
	}
	return nil
}

func (d *Deps) createPurchase(ctx context.Context, in *createPurchaseIn) (*body[Purchase], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.treasuryReady(); err != nil {
		return nil, err
	}
	plan, ok := core.PlanBySKU(in.Body.SKU)
	if !ok {
		return nil, huma.Error400BadRequest("unknown sku")
	}
	if strings.TrimSpace(in.Body.Promo) != "" {
		return nil, huma.Error400BadRequest("unknown promo code")
	}
	p, err := d.Store.CreatePurchase(ctx, store.Purchase{
		UserID: u.ID, SKU: in.Body.SKU, AmountUSDC: plan.PriceUSDC, MemoNonce: "vig-" + auth.RandomToken(8),
		Treasury: d.Config.TreasuryPubkey, USDCMint: d.Config.USDCMint, ExpiresAt: d.now().Add(PurchaseTTL),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &body[Purchase]{Body: toPurchase(p)}, nil
}

func (d *Deps) ownedPurchase(ctx context.Context, id string) (store.Purchase, error) {
	u, err := d.user(ctx)
	if err != nil {
		return store.Purchase{}, err
	}
	p, err := d.Store.GetPurchase(ctx, id)
	if err != nil {
		return p, fail(err)
	}
	if p.UserID != u.ID {
		return p, huma.Error404NotFound("not found")
	}
	return p, nil
}

func validSignature(s string) bool {
	b, err := base58.Decode(s)
	return err == nil && len(b) == 64
}

func (d *Deps) confirmPurchase(ctx context.Context, in *confirmPurchaseIn) (*body[Purchase], error) {
	p, err := d.ownedPurchase(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if p.State == "confirmed" {
		return &body[Purchase]{Body: toPurchase(p)}, nil
	}
	if p.State != "pending" || p.ExpiresAt.Before(d.now()) {
		return nil, huma.Error409Conflict("purchase is " + p.State)
	}
	if !validSignature(in.Body.TxSignature) {
		return nil, huma.Error400BadRequest("tx_signature must be a base58 transaction signature")
	}
	if d.Chain == nil {
		return nil, huma.Error503ServiceUnavailable("chain access not configured")
	}
	raw, err := d.Chain.GetTransaction(ctx, in.Body.TxSignature)
	if err != nil {
		return nil, huma.Error502BadGateway("chain read failed: " + err.Error())
	}
	slot, err := chain.CheckPayment(raw, chain.Payment{MemoNonce: p.MemoNonce, Mint: p.USDCMint, Treasury: p.Treasury, AmountBase: p.AmountUSDC})
	if err != nil {
		return nil, huma.Error400BadRequest("payment not confirmed: " + err.Error())
	}
	p, err = d.Store.ConfirmPurchase(ctx, p.ID, in.Body.TxSignature, slot)
	if err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, p.UserID, "purchase_confirmed", map[string]any{"purchase_id": p.ID, "sku": string(p.SKU)})
	return &body[Purchase]{Body: toPurchase(p)}, nil
}

func (d *Deps) getPurchase(ctx context.Context, in *idPath) (*body[Purchase], error) {
	p, err := d.ownedPurchase(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &body[Purchase]{Body: toPurchase(p)}, nil
}

// tweetText is the suggested post. It carries the nonce and no product name.
func tweetText(nonce string) string {
	return "Getting my Solana program audited against the exact bytecode on mainnet. Ref " + nonce
}

func (d *Deps) tweetNonce(ctx context.Context, _ *struct{}) (*tweetNonceOut, error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	out := &tweetNonceOut{}
	out.Body.Nonce = d.Signer.TweetNonce(u.ID)
	out.Body.Text = tweetText(out.Body.Nonce)
	return out, nil
}

func validPostURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if host != "x.com" && host != "twitter.com" && host != "mobile.twitter.com" {
		return false
	}
	return strings.Contains(u.Path, "/status/")
}

func (d *Deps) createTweetClaim(ctx context.Context, in *tweetClaimIn) (*body[TweetClaim], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	if !validPostURL(in.Body.URL) {
		return nil, huma.Error400BadRequest("url must be a post on x.com")
	}
	claims, err := d.Store.ListTweetClaims(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	for _, c := range claims {
		if c.State == "submitted" {
			return nil, huma.Error409Conflict("a claim is already waiting for review")
		}
		if c.State == "approved" {
			return nil, huma.Error409Conflict("the free audit was already granted")
		}
	}
	c, err := d.Store.CreateTweetClaim(ctx, u.ID, d.Signer.TweetNonce(u.ID), strings.TrimSpace(in.Body.URL))
	if err != nil {
		return nil, fail(err)
	}
	return &body[TweetClaim]{Body: toClaim(c)}, nil
}

func (d *Deps) listTweetClaims(ctx context.Context, _ *struct{}) (*body[list[TweetClaim]], error) {
	u, err := d.user(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := d.Store.ListTweetClaims(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]TweetClaim, 0, len(rows))
	for _, c := range rows {
		out = append(out, toClaim(c))
	}
	return items(out), nil
}
