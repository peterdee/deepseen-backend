package auth

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"

	"deepseen-backend/configuration"
	DB "deepseen-backend/database"
	Schemas "deepseen-backend/database/schemas"
	"deepseen-backend/redis"
	"deepseen-backend/utilities"
)

// Handle signing up
func signUp(ctx *fiber.Ctx) error {
	// check data
	var body SignUpUserRequest
	bodyParsingError := ctx.BodyParser(&body)
	if bodyParsingError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}
	client := body.Client
	email := body.Email
	firstName := body.FirstName
	lastName := body.LastName
	password := body.Password
	signedAgreement := body.SignedAgreement
	if client == "" || email == "" || firstName == "" ||
		lastName == "" || password == "" || !signedAgreement {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.MissingData,
			Status: fiber.StatusBadRequest,
		})
	}
	trimmedClient := strings.TrimSpace(client)
	trimmedEmail := strings.TrimSpace(email)
	trimmedFirstName := strings.TrimSpace(firstName)
	trimmedLastName := strings.TrimSpace(lastName)
	trimmedPassword := strings.TrimSpace(password)
	if trimmedClient == "" || trimmedEmail == "" || trimmedFirstName == "" ||
		trimmedLastName == "" || trimmedPassword == "" {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.MissingData,
			Status: fiber.StatusBadRequest,
		})
	}

	// make sure that email address is valid
	emailIsValid := utilities.ValidateEmail(trimmedEmail)
	if !emailIsValid {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InvalidEmail,
			Status: fiber.StatusBadRequest,
		})
	}

	// make sure that the client is valid
	clients := utilities.Values(configuration.Clients)
	if !utilities.IncludesString(clients, trimmedClient) {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InvalidData,
			Status: fiber.StatusBadRequest,
		})
	}

	// load User schema
	UserCollection := DB.Instance.Database.Collection(DB.Collections.User)

	// check if email is already in use
	existingRecord := UserCollection.FindOne(
		ctx.Context(),
		bson.D{{Key: "email", Value: trimmedEmail}},
	)
	existingUser := &Schemas.User{}
	existingRecord.Decode(existingUser)
	if existingUser.ID != "" {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.EmailAlreadyInUse,
			Status: fiber.StatusBadRequest,
		})
	}

	// create a new User record, insert it and get back the ID
	now := utilities.MakeTimestamp()
	NewUser := new(Schemas.User)
	NewUser.ID = ""
	NewUser.Email = trimmedEmail
	NewUser.FirstName = trimmedFirstName
	NewUser.LastName = trimmedLastName
	NewUser.Role = configuration.Roles.User
	NewUser.SignedAgreement = true
	NewUser.Created = now
	NewUser.Updated = now
	insertionResult, insertionError := UserCollection.InsertOne(ctx.Context(), NewUser)
	if insertionError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}
	createdRecord := UserCollection.FindOne(
		ctx.Context(),
		bson.D{{Key: "_id", Value: insertionResult.InsertedID}},
	)
	createdUser := &Schemas.User{}
	createdRecord.Decode(createdUser)

	// load Image schema
	ImageCollection := DB.Instance.Database.Collection(DB.Collections.Image)

	// create an Image for the User
	image, imageError := utilities.MakeHash(
		createdUser.ID + fmt.Sprintf("%v", utilities.MakeTimestamp()),
	)
	if imageError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	// create a new Image record and insert it
	NewImage := new(Schemas.Image)
	NewImage.ID = ""
	NewImage.Image = image
	NewImage.UserId = createdUser.ID
	NewImage.Created = now
	NewImage.Updated = now
	_, insertionError = ImageCollection.InsertOne(ctx.Context(), NewImage)
	if insertionError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	// load Password schema
	PasswordCollection := DB.Instance.Database.Collection(DB.Collections.Password)

	// create password hash
	hash, hashError := utilities.MakeHash(trimmedPassword)
	if hashError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	// create a new Password record and insert it
	NewPassword := new(Schemas.Password)
	NewPassword.ID = ""
	NewPassword.Hash = hash
	NewPassword.RecoveryCode = ""
	NewPassword.UserId = createdUser.ID
	NewPassword.Created = now
	NewPassword.Updated = now
	_, insertionError = PasswordCollection.InsertOne(ctx.Context(), NewPassword)
	if insertionError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	// generate a token
	expiration, expirationError := strconv.Atoi(os.Getenv("TOKEN_EXPIRATION"))
	if expirationError != nil {
		expiration = 9999
	}
	token, tokenError := utilities.GenerateJWT(utilities.GenerateJWTParams{
		Client:    trimmedClient,
		ExpiresIn: int64(expiration),
		Image:     image,
		UserId:    createdUser.ID,
	})
	if tokenError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	// store user image in Redis
	redisError := redis.Client.Set(
		context.Background(),
		utilities.KeyFormatter(
			configuration.Redis.Prefixes.User,
			createdUser.ID,
		),
		image,
		configuration.Redis.TTL,
	).Err()
	if redisError != nil {
		return utilities.Response(utilities.ResponseParams{
			Ctx:    ctx,
			Info:   configuration.ResponseMessages.InternalServerError,
			Status: fiber.StatusInternalServerError,
		})
	}

	formattedTemplate := utilities.CreateWelcomeTemplate(
		createdUser.FirstName,
		createdUser.LastName,
	)
	utilities.SendEmail(
		createdUser,
		"Welcome to Deepseen!",
		formattedTemplate,
	)

	return utilities.Response(utilities.ResponseParams{
		Ctx: ctx,
		Data: fiber.Map{
			"token": token,
			"user":  createdUser,
		},
	})
}
