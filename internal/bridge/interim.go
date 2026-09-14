package bridge

import (
	"context"
	"fmt"
	"time"
)

const (
	sessionStillStartingNotice = "сессия %q ещё запускается — жду уже %s"
	answerStillComingNotice    = "ещё работаю над ответом — прошло уже %s"
)

func (b *Bridge) noticeSessionStillStarting(ctx context.Context, chatID int64, name string) InterimFunc {
	return func(elapsed time.Duration) {
		b.reply(ctx, chatID, fmt.Sprintf(sessionStillStartingNotice, name, elapsed.Round(time.Second)))
	}
}

func (b *Bridge) noticeAnswerStillComing(ctx context.Context, chatID int64) InterimFunc {
	return func(elapsed time.Duration) {
		b.reply(ctx, chatID, fmt.Sprintf(answerStillComingNotice, elapsed.Round(time.Second)))
	}
}
