package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/google/uuid"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/geoip"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/osm"
	"github.com/szczursonn/mc-discord-auth-proxy/internal/players"
)

type Handler struct {
	logger *slog.Logger
	wg     sync.WaitGroup

	client      bot.Client
	players     *players.PlayerConfigurationProvider
	geoIp       *geoip.Client
	isWhitelist bool

	approveNotifiersMu sync.Mutex
	approveNotifiers   map[string]chan bool

	dmChannelIdCacheMu sync.RWMutex
	dmChannelIdCache   map[snowflake.ID]snowflake.ID
}

var ErrLoginRequestRejected = fmt.Errorf("login request rejected")
var ErrUserNotWhitelisted = fmt.Errorf("user not whitelisted")

const customIdPrefixApprove = "mc-discord-auth-proxy-approve-"
const customIdPrefixReject = "mc-discord-auth-proxy-reject-"

func NewHandler(ctx context.Context, logger *slog.Logger, discordToken string, playerConfigurationProvider *players.PlayerConfigurationProvider, geoIpClient *geoip.Client, isWhitelist bool) (*Handler, error) {
	h := &Handler{
		logger:           logger,
		players:          playerConfigurationProvider,
		geoIp:            geoIpClient,
		isWhitelist:      isWhitelist,
		approveNotifiers: map[string]chan bool{},
		dmChannelIdCache: map[snowflake.ID]snowflake.ID{},
	}

	readyChan := make(chan *events.Ready, 1)
	readyEventListener := bot.NewListenerChan(readyChan)

	var err error
	h.client, err = disgo.New(discordToken, bot.WithLogger(slog.New(slog.DiscardHandler)), bot.WithGatewayConfigOpts(gateway.WithAutoReconnect(true), gateway.WithIntents(gateway.IntentsNone)), bot.WithEventListenerFunc(h.handleComponentInteractionCreate), bot.WithEventListenerChan(readyChan))
	if err != nil {
		return nil, fmt.Errorf("failed to create discord client: %w", err)
	}

	logger.Info("Logging into Discord...")
	if err := h.client.OpenGateway(ctx); err != nil {
		h.client.Close(ctx)
		return nil, fmt.Errorf("failed to open gateway connection: %w", err)
	}
	logger.Debug("Opened Discord gateway connection")

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to await ready event: %w", ctx.Err())
	case <-readyChan:
	}

	if selfUser, ok := h.client.Caches().SelfUser(); ok {
		logger.Info("Logged into Discord", slog.Uint64("userId", uint64(selfUser.ID)), slog.String("username", selfUser.EffectiveName()))
	}
	h.client.RemoveEventListeners(readyEventListener)

	return h, nil
}

func (h *Handler) Shutdown() {
	h.wg.Wait()
	h.client.Close(context.Background())
	h.logger.Info("Auth handler has shut down")
}

func (h *Handler) RequestLogin(ctx context.Context, username string, ipAddr net.IP) error {
	const minecraftLogoURL = "https://upload.wikimedia.org/wikipedia/commons/9/91/Logo_Minecraft.png"
	const osmTileZoomLevel = 7

	userId, found := h.players.GetUserId(username)
	if !found {
		if h.isWhitelist {
			return ErrUserNotWhitelisted
		}
		return nil
	}

	dmChannelId, err := h.getDMChannelId(ctx, userId)
	if err != nil {
		return fmt.Errorf("failed to get dm channel id: %w", err)
	}

	ipAddrStr := ipAddr.String()
	requestMsgEmbedBuilder := discord.NewEmbedBuilder().SetAuthorName("Login request").SetTitle(username).SetThumbnail(minecraftLogoURL)
	if geoRes, err := h.geoIp.GeoIP(ctx, ipAddr); err != nil {
		h.logger.Error("Failed to get IP address location", slog.String("ipAddr", ipAddrStr), slog.Any("err", err))
		requestMsgEmbedBuilder = requestMsgEmbedBuilder.SetDescription(ipAddrStr)
	} else {
		requestMsgEmbedBuilder = requestMsgEmbedBuilder.SetDescriptionf("%s\n%s\n%s %s, %s", ipAddrStr, geoRes.ISP, geoRes.City, geoRes.RegionName, geoRes.Country).SetImage(osm.GetOSMTileURL(geoRes.Lat, geoRes.Lon, osmTileZoomLevel))
	}

	requestId := uuid.NewString()
	approveNotifyChan := make(chan bool, 1)
	h.approveNotifiersMu.Lock()
	h.approveNotifiers[requestId] = approveNotifyChan
	h.approveNotifiersMu.Unlock()

	requestMsg, err := h.client.Rest().CreateMessage(dmChannelId, discord.NewMessageCreateBuilder().AddEmbeds(requestMsgEmbedBuilder.Build()).AddActionRow(
		discord.NewActionRow(
			discord.NewButton(discord.ButtonStyleSuccess, "✓", customIdPrefixApprove+requestId, "", 0),
			discord.NewButton(discord.ButtonStyleDanger, "✗", customIdPrefixReject+requestId, "", 0),
		)...,
	).Build(), rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("failed to send auth request message: %w", err)
	}
	defer h.scheduleMessageCleanup(dmChannelId, requestMsg.ID)

	select {
	case isApprove := <-approveNotifyChan:
		if isApprove {
			return nil
		}
		return ErrLoginRequestRejected
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) getDMChannelId(ctx context.Context, userId snowflake.ID) (snowflake.ID, error) {
	h.dmChannelIdCacheMu.RLock()
	channelId, isCached := h.dmChannelIdCache[userId]
	h.dmChannelIdCacheMu.RUnlock()

	if isCached {
		return channelId, nil
	}

	dmChannel, err := h.client.Rest().CreateDMChannel(userId, rest.WithCtx(ctx))
	if err != nil {
		return 0, err
	}
	channelId = dmChannel.ID()

	h.dmChannelIdCacheMu.Lock()
	h.dmChannelIdCache[userId] = channelId
	defer h.dmChannelIdCacheMu.Unlock()

	return channelId, nil
}

func (h *Handler) handleComponentInteractionCreate(event *events.ComponentInteractionCreate) {
	customId := event.Data.CustomID()

	requestId, isApprove := strings.CutPrefix(customId, customIdPrefixApprove)
	if !isApprove {
		var isReject bool
		requestId, isReject = strings.CutPrefix(customId, customIdPrefixReject)
		if !isReject {
			return
		}
	}

	h.approveNotifiersMu.Lock()
	notifier := h.approveNotifiers[requestId]
	h.approveNotifiersMu.Unlock()

	if notifier != nil {
		notifier <- isApprove
	} else {
		h.logger.Warn("Received approve", slog.String("requestId", requestId), slog.Bool("result", isApprove), slog.Uint64("userId", uint64(event.User().ID)))
		h.scheduleMessageCleanup(event.Channel().ID(), event.Message.ID)
	}
}

func (h *Handler) scheduleMessageCleanup(channelId snowflake.ID, messageId snowflake.ID) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		if err := h.client.Rest().DeleteMessage(channelId, messageId); err != nil {
			h.logger.Error("Failed to clean up message", slog.Uint64("channelId", uint64(channelId)), slog.Uint64("messageId", uint64(messageId)))
		} else {
			h.logger.Debug("Cleaned up message", slog.Uint64("channelId", uint64(channelId)), slog.Uint64("messageId", uint64(messageId)))
		}
	}()
}
