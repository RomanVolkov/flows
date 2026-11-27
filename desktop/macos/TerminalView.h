#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#include <ghostty.h>

NS_ASSUME_NONNULL_BEGIN

@interface FlowsTerminalMetricsSummary : NSObject
@property(nonatomic) double durationMs;
@property(nonatomic) NSUInteger renderSubmissions;
@property(nonatomic) double submissionRateHz;
@property(nonatomic) double submissionP50GapMs;
@property(nonatomic) double submissionP95GapMs;
@property(nonatomic) double submissionMaxGapMs;
@property(nonatomic) NSUInteger missed33_4ms;
@property(nonatomic) NSUInteger displayLinkCallbacks;
@property(nonatomic) double displayLinkRateHz;
@property(nonatomic) double displayLinkP95GapMs;
@property(nonatomic) BOOL metalLayerAttached;
@property(nonatomic) NSUInteger scrollbarUpdates;
@property(nonatomic) NSUInteger distinctScrollbarOffsets;
@property(nonatomic) uint64_t initialScrolledRows;
@property(nonatomic) uint64_t finalScrolledRows;
@end

@interface FlowsGhosttyRuntime : NSObject
+ (instancetype)sharedRuntime;
@property(nonatomic, readonly, nullable) ghostty_app_t app;
- (BOOL)ensureInitialized;
- (void)shutdown;
@end

@interface TerminalView : NSView <NSTextInputClient>
@property(nonatomic) ghostty_surface_t _Nullable surface;
@property(nonatomic, readonly) BOOL ready;
@property(nonatomic, readonly) BOOL inputOwner;
@property(nonatomic, readonly) BOOL exited;
@property(nonatomic, readonly) int exitCode;
@property(nonatomic, readonly, copy, nullable) NSString *clientID;
@property(nonatomic, readonly, copy, nullable) NSString *ownerID;
@property(nonatomic, readonly, copy, nullable) NSString *fencedReason;
@property(nonatomic, readonly, copy, nullable) NSString *terminalTitle;
@property(nonatomic, readonly) uint64_t initialScrolledRows;
@property(nonatomic, readonly) uint64_t scrollbarTotal;
@property(nonatomic, readonly) uint64_t scrollbarOffset;
@property(nonatomic, readonly) uint64_t scrollbarLen;
@property(nonatomic, readonly) uint64_t scrolledRows;
@property(nonatomic, copy, nullable) void (^onReady)(void);
@property(nonatomic, copy, nullable) void (^onInputOwnerChanged)(BOOL isOwner);
@property(nonatomic, copy, nullable) void (^onExit)(int code);
@property(nonatomic, copy, nullable) void (^onFenced)(NSString *reason);
@property(nonatomic, copy, nullable) void (^onScrollbar)(uint64_t total, uint64_t offset, uint64_t len);

- (BOOL)attachWithExecutable:(NSString *)executable
                  socketPath:(NSString *)socketPath
                   sessionID:(NSString *)sessionID
                    clientID:(nullable NSString *)clientID
                       nonce:(nullable NSString *)nonce
                       epoch:(uint64_t)epoch
                    fontSize:(float)fontSize;
- (void)syncSize;
- (void)claimInput;
- (void)syncScrollOffsetToHost;
- (void)scrollLines:(NSInteger)lines;
- (void)detach;
- (NSString *)readScreenText;
- (NSString *)readViewportText;
- (void)startRenderMetrics;
- (FlowsTerminalMetricsSummary *)stopRenderMetrics;
@end

NS_ASSUME_NONNULL_END
