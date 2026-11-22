package core

import (
	"context"
	"time"

	"danny.vn/vngcloud/internal/tokencache"
)

type guardedTOTP struct {
	auth     *IAMUserAuth
	provider TOTPProvider
	cache    *tokencache.Cache
	key      tokencache.Key
}

func (g guardedTOTP) GetCode(ctx context.Context) (string, error) {
	current := timeNow().Unix() / 30
	last, present := g.auth.lastTOTPStep, g.auth.hasTOTPStep
	if g.cache != nil {
		disk, ok := g.cache.ReadTOTPStep(g.key, current)
		if ok && (!present || disk > last) {
			last, present = disk, true
		}
	}
	var step int64
	for {
		now := timeNow()
		step = now.Unix() / 30
		if !present || last < step {
			break
		}
		delay := time.Unix((step+1)*30, 0).Sub(now)
		wait := g.auth.waitTOTP
		if wait == nil {
			wait = waitForTOTP
		}
		if err := wait(ctx, delay); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var code string
	var err error
	if secret, ok := g.provider.(*SecretTOTP); ok {
		code, err = secret.codeForStep(step)
	} else {
		code, err = g.provider.GetCode(ctx)
		after := timeNow().Unix() / 30
		if after > step {
			step = after
		}
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if g.cache != nil {
		if err := g.cache.WriteTOTPStep(ctx, g.key, step); err != nil {
			return "", err
		}
	}
	g.auth.lastTOTPStep, g.auth.hasTOTPStep = step, true
	return code, nil
}

func waitForTOTP(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
