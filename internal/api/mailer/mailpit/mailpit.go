package mailpit

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wneessen/go-mail"
	"journey/internal/pgstore"
	"time"
)

type store interface {
	GetTrip(ctx context.Context, uuid2 uuid.UUID) (pgstore.Trip, error)
	GetParticipant(ctx context.Context, uuid uuid.UUID) (pgstore.Participant, error)
}

type MailPit struct {
	store store
}

func NewMailpit(pool *pgxpool.Pool) MailPit {
	return MailPit{pgstore.New(pool)}
}

func (mp MailPit) SendConfirmTripEmailToTripOwner(tripID uuid.UUID) error {
	ctx := context.Background()
	trip, err := mp.store.GetTrip(ctx, tripID)
	if err != nil {
		return fmt.Errorf("mailpit falid to get trip for SendConfirmTripEmailToTripOwner: %w", err)
	}

	msg := mail.NewMsg()
	if err := msg.From("mailpit@journey.com"); err != nil {
		return fmt.Errorf("mailpit falid to set From in email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	if err := msg.To(trip.OwnerEmail); err != nil {
		return fmt.Errorf("mailpit falid to set To in email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	msg.Subject("Confirme sua viagem!!!")

	msg.SetBodyString(mail.TypeTextPlain, fmt.Sprintf(`
		Olá, %s

		A sua viagem para %s que começa no dia %s precisa ser confirmada.
		Clique no botão abaixo para confirmar.
		`, trip.OwnerName, trip.Destination, trip.StartsAt.Time.Format(time.DateOnly),
	))

	client, err := mail.NewClient("mailpit", mail.WithTLSPortPolicy(mail.NoTLS), mail.WithPort(1025))
	if err != nil {
		return fmt.Errorf("mailpit falid to create email client in email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	if err := client.DialAndSend(msg); err != nil {
		return fmt.Errorf("mailpit falid to send email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	return nil
}

func (mp MailPit) SendConfirmTripEmailToTripParticipant(participantID uuid.UUID, tripID uuid.UUID) error {
	ctx := context.Background()
	participant, err := mp.store.GetParticipant(ctx, participantID)
	if err != nil {
		return fmt.Errorf("mailpit falid to get trip for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	msg := mail.NewMsg()
	if err := msg.From("mailpit@journey.com"); err != nil {
		return fmt.Errorf("mailpit falid to set From in email for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	if err := msg.To(participant.Email); err != nil {
		return fmt.Errorf("mailpit falid to set To in email for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	trip, err := mp.store.GetTrip(ctx, tripID)
	if err != nil {
		return fmt.Errorf("mailpit falid to get trip for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	msg.Subject("Confirme sua viagem!!!")

	msg.SetBodyString(mail.TypeTextPlain, fmt.Sprintf(`
		Olá, Convidado

		A sua viagem para %s que começa no dia %s precisa ser confirmada.
		Clique no botão abaixo para confirmar.
		`, trip.Destination, trip.StartsAt.Time.Format(time.DateOnly),
	))

	client, err := mail.NewClient("mailpit", mail.WithTLSPortPolicy(mail.NoTLS), mail.WithPort(1025))
	if err != nil {
		return fmt.Errorf("mailpit falid to create email client in email for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	if err := client.DialAndSend(msg); err != nil {
		return fmt.Errorf("mailpit falid to send email for SendConfirmTripEmailToTripParticipant: %w", err)
	}

	return nil
}
