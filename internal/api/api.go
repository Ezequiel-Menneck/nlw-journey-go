package api

import (
	"context"
	"encoding/json"
	"errors"
	openapi_types "github.com/discord-gophers/goapi-gen/types"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"journey/internal/api/spec"
	"journey/internal/pgstore"
	"net/http"
	"time"
)

type store interface {
	CreateTrip(context.Context, *pgxpool.Pool, spec.CreateTripRequest) (uuid.UUID, error)
	GetParticipant(ctx context.Context, partipantID uuid.UUID) (pgstore.Participant, error)
	ConfirmParticipant(ctx context.Context, participantID uuid.UUID) error
	GetTrip(context.Context, uuid.UUID) (pgstore.Trip, error)
	UpdateTrip(context.Context, pgstore.UpdateTripParams) error
	GetTripActivities(ctx context.Context, tripID uuid.UUID) ([]pgstore.Activity, error)
	CreateActivity(ctx context.Context, activity pgstore.CreateActivityParams) (uuid.UUID, error)
	InviteParticipantToATrip(ctx context.Context, params pgstore.InviteParticipantsToTripParams) error
	GetParticipants(ctx context.Context, tripID uuid.UUID) ([]pgstore.Participant, error)
	GetTripLinks(ctx context.Context, tripID uuid.UUID) ([]pgstore.Link, error)
	CreateTripLink(ctx context.Context, link pgstore.CreateTripLinkParams) (uuid.UUID, error)
}

type mailer interface {
	SendConfirmTripEmailToTripOwner(tripID uuid.UUID) error
}

type API struct {
	store     store
	logger    *zap.Logger
	validator *validator.Validate
	pool      *pgxpool.Pool
	mailer    mailer
}

func NewApi(pool *pgxpool.Pool, logger *zap.Logger, mailer mailer) API {
	validation := validator.New(validator.WithRequiredStructEnabled())
	return API{pgstore.New(pool), logger, validation, pool, mailer}
}

// Confirms a participant on a trip.
// (PATCH /participants/{participantId}/confirm)
func (api *API) PatchParticipantsParticipantIDConfirm(w http.ResponseWriter, r *http.Request, participantID string) *spec.Response {
	id, err := uuid.Parse(participantID)
	if err != nil {
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "uuid invalido"})
	}

	participant, err := api.store.GetParticipant(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "participant não encontrado"})
		}
		api.logger.Error("failed to get participant", zap.Error(err), zap.String("participant_id", participantID))
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	if participant.IsConfirmed {
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "participant já confirmado"})
	}

	if err := api.store.ConfirmParticipant(r.Context(), id); err != nil {
		api.logger.Error("failed to confirm participant", zap.Error(err), zap.String("participant_id", participantID))
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	return spec.PatchParticipantsParticipantIDConfirmJSON204Response(nil)
}

// Create a new trip
// (POST /trips)
func (api *API) PostTrips(w http.ResponseWriter, r *http.Request) *spec.Response {
	var body spec.CreateTripRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PostTripsJSON400Response(spec.Error{Message: "Invalid JSON: " + err.Error()})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PostTripsJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	tripID, err := api.store.CreateTrip(r.Context(), api.pool, body)
	if err != nil {
		return spec.PostTripsJSON400Response(spec.Error{Message: "failed to create trip, try again"})
	}

	go func() {
		if err := api.mailer.SendConfirmTripEmailToTripOwner(tripID); err != nil {
			api.logger.Error("failed to send email on PostTrips: %w",
				zap.Error(err),
				zap.String("trip_id", tripID.String()))
		}
	}()

	return spec.PostTripsJSON201Response(spec.CreateTripResponse{TripID: tripID.String()})
}

// Get a trip details.
// (GET /trips/{tripId})
func (api *API) GetTripsTripID(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDJSON400Response(spec.Error{Message: "uuid invalido"})
	}

	trip, err := api.store.GetTrip(r.Context(), id)
	if err != nil {
		return spec.GetTripsTripIDJSON400Response(spec.Error{Message: "This trip dont exists, try again"})
	}

	return spec.GetTripsTripIDJSON200Response(spec.GetTripDetailsResponse{Trip: spec.GetTripDetailsResponseTripObj{
		Destination: trip.Destination,
		EndsAt:      trip.EndsAt.Time,
		ID:          trip.ID.String(),
		IsConfirmed: trip.IsConfirmed,
		StartsAt:    trip.StartsAt.Time,
	}})
}

// Update a trip.
// (PUT /trips/{tripId})
func (api *API) PutTripsTripID(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "uuid invalido"})
	}

	var body spec.PutTripsTripIDJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "Invalid JSON: " + err.Error()})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	trip, err := api.store.GetTrip(r.Context(), id)
	if err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "Trip not found to update: " + err.Error()})
	}

	if err := api.store.UpdateTrip(r.Context(), pgstore.UpdateTripParams{
		Destination: body.Destination,
		EndsAt: pgtype.Timestamp{
			Time:  body.EndsAt,
			Valid: true,
		},
		StartsAt: pgtype.Timestamp{
			Time:  body.StartsAt,
			Valid: true,
		},
		IsConfirmed: trip.IsConfirmed,
		ID:          trip.ID,
	}); err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "Trip update failed"})
	}

	return nil
}

// Get a trip activities.
// (GET /trips/{tripId}/activities)
func (api *API) GetTripsTripIDActivities(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDActivitiesJSON400Response(spec.Error{Message: "uuid invalido"})
	}

	activities, err := api.store.GetTripActivities(r.Context(), id)
	if err != nil {
		return spec.GetTripsTripIDActivitiesJSON400Response(spec.Error{Message: "Activities for this trip not found: " + err.Error()})
	}

	groupedActivities := make(map[time.Time][]spec.GetTripActivitiesResponseInnerArray)

	// Itera sobre as atividades e adiciona ao slice correspondente no mapa
	for _, activity := range activities {
		date := activity.OccursAt.Time
		groupedActivities[date] = append(groupedActivities[date], spec.GetTripActivitiesResponseInnerArray{
			ID:       activity.ID.String(),
			OccursAt: activity.OccursAt.Time,
			Title:    activity.Title,
		})
	}

	// Converte o mapa em uma slice de AtividadesKey
	var groupedKeys []spec.GetTripActivitiesResponseOuterArray
	for date, activities2 := range groupedActivities {
		groupedKeys = append(groupedKeys, spec.GetTripActivitiesResponseOuterArray{
			Date:       date,
			Activities: activities2,
		})
	}

	return spec.GetTripsTripIDActivitiesJSON200Response(spec.GetTripActivitiesResponse{Activities: groupedKeys})
}

// Create a trip activity.
// (POST /trips/{tripId}/activities)
func (api *API) PostTripsTripIDActivities(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	var body spec.CreateActivityRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "Invalid JSON: " + err.Error()})
	}

	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "Invalid Id: " + err.Error()})
	}

	_, err = api.store.GetTrip(r.Context(), id)
	if err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "Somethind went wrong with this trip"})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	activityId, err := api.store.CreateActivity(r.Context(), pgstore.CreateActivityParams{
		TripID: id,
		Title:  body.Title,
		OccursAt: pgtype.Timestamp{
			Time:  body.OccursAt,
			Valid: true,
		},
	})
	if err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "Something went wrong: " + err.Error()})
	}

	return spec.PostTripsTripIDActivitiesJSON201Response(spec.CreateActivityResponse{ActivityID: activityId.String()})
}

// Confirm a trip and send e-mail invitations.
// (GET /trips/{tripId}/confirm)
func (api *API) GetTripsTripIDConfirm(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDConfirmJSON400Response(spec.Error{Message: "uuid invalido"})
	}

	trip, err := api.store.GetTrip(r.Context(), id)
	if err != nil {
		return spec.GetTripsTripIDConfirmJSON400Response(spec.Error{Message: "Trip to update not found: " + err.Error()})
	}

	err = api.store.UpdateTrip(r.Context(), pgstore.UpdateTripParams{
		Destination: trip.Destination,
		EndsAt:      pgtype.Timestamp{Valid: true, Time: trip.EndsAt.Time},
		StartsAt:    pgtype.Timestamp{Valid: true, Time: trip.StartsAt.Time},
		IsConfirmed: true,
		ID:          trip.ID,
	})
	if err != nil {
		return spec.GetTripsTripIDConfirmJSON400Response(spec.Error{Message: "Error when confirm a trip: " + err.Error()})
	}

	return spec.GetTripsTripIDConfirmJSON204Response("")
}

// Invite someone to the trip.
// (POST /trips/{tripId}/invites)
func (api *API) PostTripsTripIDInvites(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	var body spec.InviteParticipantRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PostTripsTripIDInvitesJSON400Response(spec.Error{Message: "Invalid JSON: " + err.Error()})
	}

	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.PostTripsTripIDInvitesJSON400Response(spec.Error{Message: "Invalid Id: " + err.Error()})
	}

	if err := api.store.InviteParticipantToATrip(r.Context(), pgstore.InviteParticipantsToTripParams{
		TripID: id,
		Email:  string(body.Email),
	}); err != nil {
		return spec.PostTripsTripIDInvitesJSON400Response(spec.Error{Message: "Failed to invite participant to a trip" + err.Error()})
	}

	return spec.PostTripsTripIDInvitesJSON201Response(nil)
}

// Get a trip links.
// (GET /trips/{tripId}/links)
func (api *API) GetTripsTripIDLinks(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDLinksJSON400Response(spec.Error{Message: "Invalid Trip UUID: " + err.Error()})
	}

	links, err := api.store.GetTripLinks(r.Context(), id)
	if err != nil {
		return spec.GetTripsTripIDLinksJSON400Response(spec.Error{Message: "Failed to get Trip links: " + err.Error()})
	}

	linksResponse := make([]spec.GetLinksResponseArray, len(links))
	for i, link := range links {
		linksResponse[i] = spec.GetLinksResponseArray{
			ID:    link.ID.String(),
			Title: link.Title,
			URL:   link.Url,
		}
	}

	return spec.GetTripsTripIDLinksJSON200Response(spec.GetLinksResponse{Links: linksResponse})

}

// Create a trip link.
// (POST /trips/{tripId}/links)
func (api *API) PostTripsTripIDLinks(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	var body spec.CreateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		spec.PostTripsTripIDLinksJSON400Response(spec.Error{Message: "Invalid JSON: " + err.Error()})
	}

	id, err := uuid.Parse(tripID)
	if err != nil {
		spec.PostTripsTripIDLinksJSON400Response(spec.Error{Message: "Invalid Id: " + err.Error()})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PostTripsTripIDLinksJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	linkId, err := api.store.CreateTripLink(r.Context(), pgstore.CreateTripLinkParams{
		TripID: id,
		Title:  body.Title,
		Url:    body.URL,
	})
	if err != nil {
		return spec.PostTripsTripIDLinksJSON400Response(spec.Error{Message: "Failed to create link: " + err.Error()})
	}

	return spec.PostTripsTripIDLinksJSON201Response(spec.CreateLinkResponse{LinkID: linkId.String()})
}

// Get a trip participants.
// (GET /trips/{tripId}/participants)
func (api *API) GetTripsTripIDParticipants(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDParticipantsJSON400Response(spec.Error{Message: "Trip id invalid: " + err.Error()})
	}

	participants, err := api.store.GetParticipants(r.Context(), id)
	if err != nil {
		return spec.GetTripsTripIDParticipantsJSON400Response(spec.Error{Message: "Error to get the trip participants: " + err.Error()})
	}

	getTripParticipantsResponseArray := make([]spec.GetTripParticipantsResponseArray, len(participants))
	for i, participant := range participants {
		getTripParticipantsResponseArray[i] = spec.GetTripParticipantsResponseArray{
			Email:       openapi_types.Email(participant.Email),
			ID:          participant.ID.String(),
			IsConfirmed: participant.IsConfirmed,
			Name:        nil,
		}

	}

	return spec.GetTripsTripIDParticipantsJSON200Response(spec.GetTripParticipantsResponse{Participants: getTripParticipantsResponseArray})
}
