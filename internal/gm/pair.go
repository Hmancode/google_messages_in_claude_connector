package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/util/exhttp"
)

// Pairing is an in-progress Google account pairing.
type Pairing struct {
	raw    *libgm.Client
	api    api
	ps     *libgm.PairingSession
	cancel context.CancelFunc
}

// StartPairing signs in with the given Google cookies and asks the phone to
// show an emoji. It returns the emoji the user must tap on the phone.
func StartPairing(ctx context.Context, cookies map[string]string, libgmLog zerolog.Logger) (*Pairing, string, error) {
	ad := libgm.NewAuthData()
	ad.SetCookies(cookies)
	raw := libgm.NewClient(ad, nil, libgmLog, exhttp.SensibleClientSettings)
	p := &Pairing{raw: raw, api: raw}
	p.api.SetEventHandler(func(any) {})
	if err := p.api.FetchConfig(ctx); err != nil {
		return nil, "", fmt.Errorf("Google did not accept the cookies (are you signed in at messages.google.com in that window?): %w", err)
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	emoji, ps, err := p.api.StartGaiaPairing(ctx, bgCtx)
	if err != nil {
		cancel()
		p.api.Disconnect()
		return nil, "", friendlyPairError(err)
	}
	p.ps = ps
	return p, emoji, nil
}

// Wait blocks until the emoji is tapped on the phone, then returns the session
// credential to save.
func (p *Pairing) Wait(ctx context.Context) ([]byte, error) {
	defer p.cancel()
	_, err := p.api.FinishGaiaPairing(ctx, p.ps)
	p.api.Disconnect()
	if err != nil {
		return nil, friendlyPairError(err)
	}
	return json.Marshal(p.raw.AuthData)
}

// Cancel abandons the pairing.
func (p *Pairing) Cancel() {
	p.cancel()
	p.api.Disconnect()
}

// EmojiImageURL returns a link to a picture of the emoji, for terminals that
// cannot draw emoji.
func EmojiImageURL(emoji string) string { return libgm.GetEmojiSVG(emoji) }

// RefreshCookies replaces the Google cookies in an existing session without
// re-pairing the phone. It checks the cookies belong to the same account.
func RefreshCookies(ctx context.Context, authJSON []byte, cookies map[string]string, libgmLog zerolog.Logger) ([]byte, error) {
	ad := libgm.NewAuthData()
	if err := json.Unmarshal(authJSON, ad); err != nil {
		return nil, fmt.Errorf("saved session is unreadable: %w", err)
	}
	ad.SetCookies(cookies)
	raw := libgm.NewClient(ad, nil, libgmLog, exhttp.SensibleClientSettings)
	var a api = raw
	a.SetEventHandler(func(any) {})
	if err := a.FetchConfig(ctx); err != nil {
		return nil, fmt.Errorf("Google did not accept the new cookies: %w", err)
	}
	want := strings.ToLower(ad.Mobile.GetSourceID())
	got := strings.ToLower(raw.Config.GetDeviceInfo().GetEmail())
	if want != "" && got != "" && want != got {
		return nil, errors.New("those cookies are for a different Google account than the one that was paired")
	}
	if err := a.Connect(ctx); err != nil {
		a.Disconnect()
		return nil, fmt.Errorf("could not reconnect with the new cookies: %w", err)
	}
	a.Disconnect()
	return json.Marshal(raw.AuthData)
}

func friendlyPairError(err error) error {
	switch {
	case errors.Is(err, libgm.ErrNoDevicesFound):
		return errors.New("no phone found. In Google Messages on your phone, make sure Device pairing is turned on for this Google account")
	case errors.Is(err, libgm.ErrPhoneNotFound):
		return errors.New("your phone isn't ready for pairing. Open Google Messages on your phone, then try again")
	case errors.Is(err, libgm.ErrPhoneInactive):
		return errors.New("Google Messages on your phone hasn't connected to this Google account recently. On the phone: Google Messages > profile picture > Device pairing, check it uses the same Google account, then try again")
	case errors.Is(err, libgm.ErrPairingInitTimeout):
		return errors.New("your phone didn't respond. Make sure it is online, open Google Messages on it, and try again")
	case errors.Is(err, events.ErrCallerNoPermission):
		return errors.New("that Google account isn't allowed to use Google Messages for Web")
	case errors.Is(err, libgm.ErrIncorrectEmoji):
		return errors.New("the wrong emoji was tapped on the phone. Run pairing again")
	case errors.Is(err, libgm.ErrPairingCancelled):
		return errors.New("pairing was cancelled on the phone")
	case errors.Is(err, libgm.ErrPairingTimeout):
		return errors.New("pairing timed out. Run it again and tap the emoji sooner")
	}
	return fmt.Errorf("pairing failed: %w", err)
}
