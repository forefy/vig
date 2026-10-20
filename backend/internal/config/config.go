// Package config reads every runtime setting from the environment. Secrets
// never come from anywhere else.
package config

import (
	"errors"
	"os"
	"strings"
)

// USDC mint on Solana mainnet, used when USDC_MINT is not set.
const DefaultUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// Public mainnet RPC, used as the fallback and as the primary when no keyed
// endpoint is configured.
const PublicRPC = "https://api.mainnet-beta.solana.com"

// PublicRPC2 is a second keyless endpoint; the client falls through to it
// when the first one throttles.
const PublicRPC2 = "https://solana-rpc.publicnode.com"

// Config is the process configuration.
type Config struct {
	Env           string // dev or prod (VIG_ENV)
	Port          string
	DatabaseURL   string
	SessionSecret string
	FrontendURL   string
	BackendURL    string
	SiteURL       string // canonical public origin for meta tags and OG images (SITE_URL, defaults to FrontendURL)
	DataDir       string
	StaticDir     string // built frontend to serve; empty means API only (STATIC_DIR)
	LogFormat     string // json or text (LOG_FORMAT)

	GoogleClientID         string
	GoogleClientSecret     string
	GitHubClientID         string
	GitHubClientSecret     string
	DiscordClientID        string
	DiscordClientSecret    string
	DiscordBotToken        string
	DiscordGuildID         string
	DiscordInviteURL       string
	GitHubAppID            string
	GitHubAppSlug          string
	GitHubAppPrivateKey    string
	GitHubAppWebhookSecret string
	GitHubToken            string

	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	RPCURLs []string
	// EVMRPC overrides the public endpoint of an EVM chain, by chain id
	// (EVM_RPC_1, EVM_RPC_8453, ...).
	EVMRPC    map[string]string
	OsecURL   string
	GitHubAPI string

	TreasuryPubkey string
	USDCMint       string
}

// FromEnv loads the configuration. Only DATABASE_URL and SESSION_SECRET are
// required to serve; everything else degrades to a clear 503 problem.
func FromEnv() (Config, error) {
	c := Config{
		Env:                    env("VIG_ENV", "dev"),
		Port:                   env("PORT", "7112"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		SessionSecret:          os.Getenv("SESSION_SECRET"),
		FrontendURL:            strings.TrimRight(env("FRONTEND_URL", "http://localhost:7111"), "/"),
		BackendURL:             strings.TrimRight(env("BACKEND_URL", "http://localhost:"+env("PORT", "7112")), "/"),
		DataDir:                env("DATA_DIR", "./data"),
		StaticDir:              os.Getenv("STATIC_DIR"),
		GoogleClientID:         os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:     os.Getenv("GOOGLE_CLIENT_SECRET"),
		GitHubClientID:         os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		GitHubClientSecret:     os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		DiscordClientID:        os.Getenv("DISCORD_CLIENT_ID"),
		DiscordClientSecret:    os.Getenv("DISCORD_CLIENT_SECRET"),
		DiscordBotToken:        os.Getenv("DISCORD_BOT_TOKEN"),
		DiscordGuildID:         os.Getenv("DISCORD_GUILD_ID"),
		DiscordInviteURL:       os.Getenv("DISCORD_INVITE_URL"),
		GitHubAppID:            os.Getenv("GITHUB_APP_ID"),
		GitHubAppSlug:          os.Getenv("GITHUB_APP_SLUG"),
		GitHubAppPrivateKey:    os.Getenv("GITHUB_APP_PRIVATE_KEY"),
		GitHubAppWebhookSecret: os.Getenv("GITHUB_APP_WEBHOOK_SECRET"),
		SMTPHost:               os.Getenv("SMTP_HOST"),
		SMTPPort:               env("SMTP_PORT", "587"),
		SMTPUser:               os.Getenv("SMTP_USER"),
		SMTPPass:               os.Getenv("SMTP_PASS"),
		SMTPFrom:               os.Getenv("SMTP_FROM"),
		GitHubToken:            os.Getenv("GITHUB_TOKEN"),
		OsecURL:                env("OSEC_VERIFY_URL", "https://verify.osec.io"),
		GitHubAPI:              env("GITHUB_API_URL", "https://api.github.com"),
		TreasuryPubkey:         os.Getenv("TREASURY_PUBKEY"),
		USDCMint:               env("USDC_MINT", DefaultUSDCMint),
	}
	c.SiteURL = strings.TrimRight(env("SITE_URL", c.FrontendURL), "/")
	c.LogFormat = env("LOG_FORMAT", map[bool]string{true: "json", false: "text"}[c.IsProd()])
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return c, errors.New("LOG_FORMAT must be json or text")
	}
	if p := os.Getenv("RPC_PRIMARY_URL"); p != "" {
		c.RPCURLs = append(c.RPCURLs, p)
	}
	c.RPCURLs = append(c.RPCURLs, env("RPC_FALLBACK_URL", PublicRPC))
	// A second keyless endpoint absorbs the first one's throttling; set
	// RPC_FALLBACK_URL to something else and this still rides behind it.
	if !contains(c.RPCURLs, PublicRPC2) {
		c.RPCURLs = append(c.RPCURLs, PublicRPC2)
	}
	c.EVMRPC = map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "EVM_RPC_") && v != "" {
			c.EVMRPC[strings.TrimPrefix(k, "EVM_RPC_")] = v
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.SessionSecret == "" {
		return c, errors.New("SESSION_SECRET is required")
	}
	return c, nil
}

// IsProd reports whether the process runs with VIG_ENV=prod.
func (c Config) IsProd() bool { return c.Env == "prod" }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
