#import "TerminalView.h"
#import <CoreGraphics/CoreGraphics.h>
#import <CoreText/CoreText.h>
#include <mach/mach_time.h>
#include <errno.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>
#include <fcntl.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wnullability-completeness"

static uint64_t gCtlSeq = 0;
static int gLayerContentsKVOContext = 0;

static double monotonicNowMs(void) {
    static mach_timebase_info_data_t info = {0};
    if (info.denom == 0) {
        mach_timebase_info(&info);
    }
    uint64_t ticks = mach_absolute_time();
    return ((double)ticks * (double)info.numer / (double)info.denom) / 1e6;
}

static NSString *shellQuote(NSString *value) {
    NSString *escaped = [value stringByReplacingOccurrencesOfString:@"'" withString:@"'\"'\"'"];
    return [NSString stringWithFormat:@"'%@'", escaped];
}

static ghostty_input_mods_e inputMods(NSEvent *event) {
    NSEventModifierFlags flags = event.modifierFlags;
    unsigned mods = 0;
    if (flags & NSEventModifierFlagShift) mods |= GHOSTTY_MODS_SHIFT;
    if (flags & NSEventModifierFlagControl) mods |= GHOSTTY_MODS_CTRL;
    if (flags & NSEventModifierFlagOption) mods |= GHOSTTY_MODS_ALT;
    if (flags & NSEventModifierFlagCommand) mods |= GHOSTTY_MODS_SUPER;
    if (flags & NSEventModifierFlagCapsLock) mods |= GHOSTTY_MODS_CAPS;
    return (ghostty_input_mods_e)mods;
}

static ghostty_input_scroll_mods_t scrollMods(NSEvent *event) {
    int mods = event.hasPreciseScrollingDeltas ? 1 : 0;
    ghostty_input_mouse_momentum_e momentum = GHOSTTY_MOUSE_MOMENTUM_NONE;
    switch (event.momentumPhase) {
        case NSEventPhaseBegan: momentum = GHOSTTY_MOUSE_MOMENTUM_BEGAN; break;
        case NSEventPhaseStationary: momentum = GHOSTTY_MOUSE_MOMENTUM_STATIONARY; break;
        case NSEventPhaseChanged: momentum = GHOSTTY_MOUSE_MOMENTUM_CHANGED; break;
        case NSEventPhaseEnded: momentum = GHOSTTY_MOUSE_MOMENTUM_ENDED; break;
        case NSEventPhaseCancelled: momentum = GHOSTTY_MOUSE_MOMENTUM_CANCELLED; break;
        case NSEventPhaseMayBegin: momentum = GHOSTTY_MOUSE_MOMENTUM_MAY_BEGIN; break;
        default: break;
    }
    mods |= ((int)momentum << 1);
    return (ghostty_input_scroll_mods_t)mods;
}

static double percentileMs(NSArray<NSNumber *> *sorted, double p) {
    if (sorted.count == 0) return 0.0;
    double idx = p * (double)(sorted.count - 1);
    NSUInteger lo = (NSUInteger)floor(idx);
    NSUInteger hi = (NSUInteger)ceil(idx);
    double frac = idx - (double)lo;
    return sorted[lo].doubleValue * (1.0 - frac) + sorted[hi].doubleValue * frac;
}

@implementation FlowsTerminalMetricsSummary
@end

@interface TerminalView ()
@property(nonatomic, copy, nullable) NSString *marked;
@property(nonatomic) BOOL interpreting;
@property(nonatomic, copy, nullable) NSString *commit;
@property(nonatomic, readwrite) BOOL ready;
@property(nonatomic, readwrite) BOOL inputOwner;
@property(nonatomic, readwrite) BOOL exited;
@property(nonatomic, readwrite) int exitCode;
@property(nonatomic, readwrite, copy, nullable) NSString *clientID;
@property(nonatomic, readwrite, copy, nullable) NSString *ownerID;
@property(nonatomic, readwrite, copy, nullable) NSString *fencedReason;
@property(nonatomic, readwrite, copy, nullable) NSString *terminalTitle;
@property(nonatomic, readwrite) uint64_t initialScrolledRows;
@property(nonatomic, readwrite) uint64_t scrollbarTotal;
@property(nonatomic, readwrite) uint64_t scrollbarOffset;
@property(nonatomic, readwrite) uint64_t scrollbarLen;
@property(nonatomic, readwrite) uint64_t scrolledRows;
@property(nonatomic, copy, nullable) NSString *ctlSocketPath;
@property(nonatomic) int ctlListenFd;
@property(nonatomic) int ctlConnFd;
@property(nonatomic, strong, nullable) dispatch_source_t ctlAcceptSource;
@property(nonatomic, strong, nullable) dispatch_source_t ctlReadSource;
@property(nonatomic, strong, nullable) NSMutableData *ctlBuffer;
@property(nonatomic) BOOL metricsActive;
@property(nonatomic) double metricsStartMs;
@property(nonatomic) uint64_t metricsInitialScrolled;
@property(nonatomic, strong, nullable) NSMutableArray<NSNumber *> *renderTimestamps;
@property(nonatomic, strong, nullable) NSMutableArray<NSNumber *> *displayLinkTimestamps;
@property(nonatomic, strong, nullable) NSMutableArray<NSNumber *> *scrollbarOffsetHistory;
@property(nonatomic, strong, nullable) CADisplayLink *displayLink;
@property(nonatomic, strong, nullable) CALayer *observedLayer;
- (void)recordRenderSubmission;
- (void)handleScrollbarUpdate:(ghostty_action_scrollbar_s)sb;
- (void)handleTitleUpdate:(NSString *)title;
@end

@interface FlowsGhosttyRuntime ()
@property(nonatomic, readwrite, nullable) ghostty_app_t app;
@property(nonatomic) BOOL initialized;
@end

static void wakeupCallback(void *context) {
    FlowsGhosttyRuntime *runtime = (__bridge FlowsGhosttyRuntime *)context;
    dispatch_async(dispatch_get_main_queue(), ^{
        if (runtime.app) ghostty_app_tick(runtime.app);
    });
}

static bool actionCallback(ghostty_app_t app, ghostty_target_s target, ghostty_action_s action) {
    (void)app;
    if (target.tag != GHOSTTY_TARGET_SURFACE) {
        return false;
    }
    TerminalView *view = (__bridge TerminalView *)ghostty_surface_userdata(target.target.surface);
    switch (action.tag) {
        case GHOSTTY_ACTION_RENDER:
            [view recordRenderSubmission];
            ghostty_surface_draw(target.target.surface);
            return true;
        case GHOSTTY_ACTION_SCROLLBAR:
            [view handleScrollbarUpdate:action.action.scrollbar];
            return true;
        case GHOSTTY_ACTION_SET_TITLE:
            if (action.action.set_title.title) {
                NSString *title = [NSString stringWithUTF8String:action.action.set_title.title];
                [view handleTitleUpdate:title];
            }
            return true;
        default:
            return false;
    }
}

static ghostty_clipboard_read_result_e readClipboardCallback(void *context, ghostty_clipboard_e clipboard,
    void *request, const char *const *types, size_t count, bool confirmed) {
    (void)context; (void)clipboard; (void)request; (void)types; (void)count; (void)confirmed;
    return GHOSTTY_CLIPBOARD_READ_UNSUPPORTED;
}

static void confirmClipboardCallback(void *context, const ghostty_clipboard_confirm_s *data,
    void *request, ghostty_clipboard_request_e kind) {
    (void)data; (void)kind;
    TerminalView *view = (__bridge TerminalView *)context;
    if (view.surface) ghostty_surface_deny_clipboard_request(view.surface, request);
}

static void writeClipboardCallback(void *context, ghostty_clipboard_e clipboard,
    const ghostty_clipboard_content_s *data, size_t count, bool confirmed) {
    (void)context; (void)clipboard; (void)confirmed;
    for (size_t i = 0; i < count; i++) {
        if (data[i].mime && strcmp(data[i].mime, "text/plain") == 0 && data[i].data && data[i].len > 0) {
            NSString *str = [[NSString alloc] initWithBytes:data[i].data length:data[i].len encoding:NSUTF8StringEncoding];
            if (str) {
                [NSPasteboard.generalPasteboard clearContents];
                [NSPasteboard.generalPasteboard setString:str forType:NSPasteboardTypeString];
            }
            break;
        }
    }
}

@implementation FlowsGhosttyRuntime

+ (instancetype)sharedRuntime {
    static FlowsGhosttyRuntime *instance = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        instance = [[FlowsGhosttyRuntime alloc] init];
    });
    return instance;
}

+ (void)registerBundledFontsOnce {
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        NSURL *fontsDir = [NSBundle.mainBundle.resourceURL URLByAppendingPathComponent:@"fonts" isDirectory:YES];
        if (!fontsDir) return;
        NSArray<NSURL *> *items = [NSFileManager.defaultManager contentsOfDirectoryAtURL:fontsDir
                                                              includingPropertiesForKeys:nil
                                                                                 options:NSDirectoryEnumerationSkipsHiddenFiles
                                                                                   error:nil];
        for (NSURL *url in items) {
            NSString *ext = url.pathExtension.lowercaseString;
            if ([ext isEqualToString:@"ttf"] || [ext isEqualToString:@"otf"]) {
                CTFontManagerRegisterFontsForURL((__bridge CFURLRef)url, kCTFontManagerScopeProcess, nil);
            }
        }
    });
}

- (ghostty_config_t)buildConfig:(nullable NSString *)configText {
    ghostty_config_t config = ghostty_config_new();
    if (configText.length > 0) {
        NSString *tmpPath = [NSTemporaryDirectory() stringByAppendingPathComponent:
            [NSString stringWithFormat:@"flows-ghostty-%d.conf", getpid()]];
        if ([configText writeToFile:tmpPath atomically:YES encoding:NSUTF8StringEncoding error:nil]) {
            ghostty_config_load_file(config, tmpPath.UTF8String);
            unlink(tmpPath.UTF8String);
        }
    }
    ghostty_config_finalize(config);
    return config;
}

- (BOOL)ensureInitialized {
    return [self applyGhosttyConfig:nil];
}

- (BOOL)applyGhosttyConfig:(nullable NSString *)configText {
    [FlowsGhosttyRuntime registerBundledFontsOnce];
    if (!self.initialized) {
        if (ghostty_init(0, NULL) != 0) return NO;
        self.initialized = YES;
    }
    if (self.app) {
        if (configText.length == 0) return YES;
        ghostty_config_t config = [self buildConfig:configText];
        ghostty_app_update_config(self.app, config);
        ghostty_config_free(config);
        return YES;
    }
    ghostty_config_t config = [self buildConfig:configText];
    ghostty_runtime_config_s runtime = {
        .userdata = (__bridge void *)self,
        .wakeup_cb = wakeupCallback,
        .action_cb = actionCallback,
        .read_clipboard_cb = readClipboardCallback,
        .confirm_read_clipboard_cb = confirmClipboardCallback,
        .write_clipboard_cb = writeClipboardCallback,
    };
    self.app = ghostty_app_new(&runtime, config);
    ghostty_config_free(config);
    return self.app != NULL;
}

- (void)shutdown {
    if (self.app) {
        ghostty_app_free(self.app);
        self.app = NULL;
    }
}

@end

@implementation TerminalView

- (instancetype)initWithFrame:(NSRect)frameRect {
    self = [super initWithFrame:frameRect];
    if (self) {
        _ctlListenFd = -1;
        _ctlConnFd = -1;
        _ctlBuffer = [NSMutableData data];
    }
    return self;
}

- (void)dealloc {
    [self detach];
}

- (BOOL)acceptsFirstResponder { return YES; }
- (BOOL)isFlipped { return YES; }

- (BOOL)becomeFirstResponder {
    if (self.surface) ghostty_surface_set_focus(self.surface, true);
    return YES;
}

- (BOOL)resignFirstResponder {
    if (self.surface) ghostty_surface_set_focus(self.surface, false);
    return YES;
}

- (void)viewDidChangeBackingProperties {
    [super viewDidChangeBackingProperties];
    [self syncSize];
}

- (void)setFrameSize:(NSSize)size {
    [super setFrameSize:size];
    [self syncSize];
}

- (void)syncSize {
    if (!self.surface || !self.window) return;
    NSSize pixels = [self convertSizeToBacking:self.bounds.size];
    if (pixels.width <= 0 || pixels.height <= 0) return;
    double scale = self.window.backingScaleFactor;
    NSNumber *display = self.window.screen.deviceDescription[@"NSScreenNumber"];
    if (display) ghostty_surface_set_display_id(self.surface, display.unsignedIntValue);
    ghostty_surface_set_content_scale(self.surface, scale, scale);
    ghostty_surface_set_size(self.surface, (uint32_t)pixels.width, (uint32_t)pixels.height);
}

- (BOOL)setupControlSocket {
    NSString *path = [NSString stringWithFormat:@"/tmp/flctl-%d-%llu.sock", getpid(), ++gCtlSeq];
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return NO;
    fcntl(fd, F_SETFL, O_NONBLOCK);
    struct sockaddr_un addr = {0};
    addr.sun_family = AF_UNIX;
    if (strlen(path.UTF8String) >= sizeof(addr.sun_path)) {
        close(fd);
        return NO;
    }
    strcpy(addr.sun_path, path.UTF8String);
    unlink(addr.sun_path);
    if (bind(fd, (struct sockaddr *)&addr, sizeof(addr)) != 0 ||
        chmod(addr.sun_path, 0600) != 0 ||
        listen(fd, 4) != 0) {
        unlink(addr.sun_path);
        close(fd);
        return NO;
    }
    self.ctlSocketPath = path;
    self.ctlListenFd = fd;

    __weak typeof(self) weakSelf = self;
    dispatch_source_t acceptSource = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, (uintptr_t)fd, 0, dispatch_get_main_queue());
    self.ctlAcceptSource = acceptSource;
    dispatch_source_set_event_handler(acceptSource, ^{
        typeof(self) strongSelf = weakSelf;
        if (!strongSelf || strongSelf.ctlListenFd < 0) return;
        int clientFd = accept(strongSelf.ctlListenFd, NULL, NULL);
        if (clientFd < 0) return;
        fcntl(clientFd, F_SETFL, O_NONBLOCK);
        strongSelf.ctlConnFd = clientFd;
        strongSelf.ctlListenFd = -1;
        dispatch_source_cancel(acceptSource);
        strongSelf.ctlAcceptSource = nil;
        [strongSelf startControlReader:clientFd];
    });
    dispatch_source_set_cancel_handler(acceptSource, ^{
        close(fd);
    });
    dispatch_resume(acceptSource);
    return YES;
}

- (void)startControlReader:(int)connFd {
    __weak typeof(self) weakSelf = self;
    dispatch_source_t readSource = dispatch_source_create(DISPATCH_SOURCE_TYPE_READ, (uintptr_t)connFd, 0, dispatch_get_main_queue());
    self.ctlReadSource = readSource;
    dispatch_source_set_event_handler(readSource, ^{
        typeof(self) strongSelf = weakSelf;
        if (!strongSelf) return;
        uint8_t buf[4096];
        for (;;) {
            ssize_t n = read(connFd, buf, sizeof(buf));
            if (n > 0) {
                [strongSelf.ctlBuffer appendBytes:buf length:(NSUInteger)n];
                [strongSelf drainControlLines];
            } else if (n == 0 || (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR)) {
                dispatch_source_cancel(readSource);
                strongSelf.ctlReadSource = nil;
                strongSelf.ctlConnFd = -1;
                break;
            } else {
                break;
            }
        }
    });
    dispatch_source_set_cancel_handler(readSource, ^{
        close(connFd);
    });
    dispatch_resume(readSource);
}

- (void)drainControlLines {
    for (;;) {
        NSRange nl = [self.ctlBuffer rangeOfData:[NSData dataWithBytes:"\n" length:1]
                                         options:0
                                           range:NSMakeRange(0, self.ctlBuffer.length)];
        if (nl.location == NSNotFound) break;
        NSData *line = [self.ctlBuffer subdataWithRange:NSMakeRange(0, nl.location)];
        [self.ctlBuffer replaceBytesInRange:NSMakeRange(0, nl.location + 1) withBytes:NULL length:0];
        if (line.length == 0) continue;
        NSDictionary *evt = [NSJSONSerialization JSONObjectWithData:line options:0 error:nil];
        if (![evt isKindOfClass:NSDictionary.class]) continue;
        [self handleControlEvent:evt];
    }
}

- (void)handleControlEvent:(NSDictionary *)evt {
    NSString *type = evt[@"type"];
    if ([type isEqualToString:@"ready"]) {
        if ([evt[@"client_id"] isKindOfClass:NSString.class]) {
            self.clientID = evt[@"client_id"];
        }
        NSNumber *scrolled = evt[@"scrolled"];
        self.initialScrolledRows = scrolled ? scrolled.unsignedLongLongValue : 0;
        self.ready = YES;
        if (self.initialScrolledRows > 0 && self.surface) {
            [self scrollLines:-(NSInteger)self.initialScrolledRows];
        }
        if (self.onReady) self.onReady();
    } else if ([type isEqualToString:@"input_owner"]) {
        self.ownerID = [evt[@"owner_id"] isKindOfClass:NSString.class] ? evt[@"owner_id"] : @"";
        BOOL isOwner = [evt[@"is_owner"] boolValue];
        self.inputOwner = isOwner;
        if (self.onInputOwnerChanged) self.onInputOwnerChanged(isOwner);
    } else if ([type isEqualToString:@"fenced"]) {
        NSString *reason = [evt[@"reason"] isKindOfClass:NSString.class] ? evt[@"reason"] : @"unknown";
        self.fencedReason = reason;
        if (self.onFenced) self.onFenced(reason);
    } else if ([type isEqualToString:@"exit"]) {
        self.exited = YES;
        self.exitCode = [evt[@"exit_code"] intValue];
        if (self.onExit) self.onExit(self.exitCode);
    }
}

- (void)sendControlCommand:(NSDictionary *)cmd {
    if (self.ctlConnFd < 0) return;
    NSData *json = [NSJSONSerialization dataWithJSONObject:cmd options:0 error:nil];
    if (!json) return;
    NSMutableData *line = [json mutableCopy];
    [line appendBytes:"\n" length:1];
    const uint8_t *bytes = line.bytes;
    size_t remaining = line.length;
    while (remaining > 0) {
        ssize_t written = write(self.ctlConnFd, bytes, remaining);
        if (written <= 0) break;
        bytes += written;
        remaining -= (size_t)written;
    }
}

- (BOOL)attachWithExecutable:(NSString *)executable
                  socketPath:(NSString *)socketPath
                   sessionID:(NSString *)sessionID
                    clientID:(nullable NSString *)clientID
                       nonce:(nullable NSString *)nonce
                       epoch:(uint64_t)epoch
                    fontSize:(float)fontSize {
    return [self attachWithExecutable:executable
                           subcommand:@"attach"
                           socketPath:socketPath
                            sessionID:sessionID
                             clientID:clientID
                                nonce:nonce
                                epoch:epoch
                             fontSize:fontSize];
}

- (BOOL)attachWithExecutable:(NSString *)executable
                  subcommand:(NSString *)subcommand
                  socketPath:(NSString *)socketPath
                   sessionID:(NSString *)sessionID
                    clientID:(nullable NSString *)clientID
                       nonce:(nullable NSString *)nonce
                       epoch:(uint64_t)epoch
                    fontSize:(float)fontSize {
    if (![FlowsGhosttyRuntime.sharedRuntime ensureInitialized]) return NO;
    [self detach];

    if (![self setupControlSocket]) return NO;
    self.ready = NO;
    self.inputOwner = NO;
    self.exited = NO;
    self.fencedReason = nil;
    self.clientID = clientID ?: [NSString stringWithFormat:@"native-%d-%llu", getpid(), gCtlSeq];

    NSString *cmd = [NSString stringWithFormat:@"%@ %@ %@ %@",
        shellQuote(executable), subcommand, shellQuote(socketPath), shellQuote(sessionID)];

    NSMutableArray<NSString *> *keys = [NSMutableArray arrayWithObjects:@"FLOWS_ATTACH_CTL", @"FLOWS_ATTACH_CLIENT_ID", nil];
    NSMutableArray<NSString *> *vals = [NSMutableArray arrayWithObjects:self.ctlSocketPath, self.clientID, nil];
    if (nonce.length > 0) {
        [keys addObject:@"FLOWS_ATTACH_NONCE"];
        [vals addObject:nonce];
    }
    if (epoch > 0) {
        [keys addObject:@"FLOWS_ATTACH_EPOCH"];
        [vals addObject:[NSString stringWithFormat:@"%llu", epoch]];
    }
    ghostty_env_var_s envVars[4];
    for (NSUInteger i = 0; i < keys.count; i++) {
        envVars[i].key = keys[i].UTF8String;
        envVars[i].value = vals[i].UTF8String;
    }

    ghostty_surface_config_s cfg = ghostty_surface_config_new();
    cfg.platform_tag = GHOSTTY_PLATFORM_MACOS;
    cfg.platform.macos.nsview = (__bridge void *)self;
    cfg.userdata = (__bridge void *)self;
    cfg.scale_factor = self.window ? self.window.backingScaleFactor : NSScreen.mainScreen.backingScaleFactor;
    cfg.font_size = fontSize;
    cfg.command = cmd.UTF8String;
    cfg.env_vars = envVars;
    cfg.env_var_count = keys.count;
    cfg.wait_after_command = true;

    self.surface = ghostty_surface_new(FlowsGhosttyRuntime.sharedRuntime.app, &cfg);
    if (!self.surface) {
        [self detach];
        return NO;
    }
    [self syncSize];
    return YES;
}

- (void)claimInput {
    [self sendControlCommand:@{@"type": @"claim_input"}];
}

- (void)syncScrollOffsetToHost {
    [self sendControlCommand:@{@"type": @"scroll_offset", @"rows": @(self.scrolledRows)}];
}

- (void)scrollLines:(NSInteger)lines {
    if (!self.surface || lines == 0) return;
    NSString *action = [NSString stringWithFormat:@"scroll_page_lines:%ld", (long)lines];
    ghostty_surface_binding_action(self.surface, action.UTF8String, [action lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
}

- (void)detach {
    [self stopRenderMetrics];
    if (self.ctlConnFd >= 0) {
        if (self.ready) {
            [self syncScrollOffsetToHost];
        }
        [self sendControlCommand:@{@"type": @"detach"}];
    }
    [self.ctlBuffer setLength:0];
    if (self.ctlReadSource) {
        dispatch_source_cancel(self.ctlReadSource);
        self.ctlReadSource = nil;
        self.ctlConnFd = -1;
    } else if (self.ctlConnFd >= 0) {
        close(self.ctlConnFd);
        self.ctlConnFd = -1;
    }
    if (self.ctlAcceptSource) {
        dispatch_source_cancel(self.ctlAcceptSource);
        self.ctlAcceptSource = nil;
        self.ctlListenFd = -1;
    } else if (self.ctlListenFd >= 0) {
        close(self.ctlListenFd);
        self.ctlListenFd = -1;
    }
    if (self.ctlSocketPath.length > 0) {
        unlink(self.ctlSocketPath.UTF8String);
        self.ctlSocketPath = nil;
    }
    if (self.surface) {
        ghostty_surface_free(self.surface);
        self.surface = NULL;
    }
    self.inputOwner = NO;
}

- (void)recordRenderSubmission {
    if (self.metricsActive) {
        [self.renderTimestamps addObject:@(monotonicNowMs())];
    }
}

- (void)handleScrollbarUpdate:(ghostty_action_scrollbar_s)sb {
    self.scrollbarTotal = sb.total;
    self.scrollbarOffset = sb.offset;
    self.scrollbarLen = sb.len;
    self.scrolledRows = (sb.total > sb.offset + sb.len) ? (sb.total - sb.offset - sb.len) : 0;
    if (self.metricsActive) {
        [self.scrollbarOffsetHistory addObject:@(sb.offset)];
    }
    if (self.onScrollbar) self.onScrollbar(sb.total, sb.offset, sb.len);
}

- (void)handleTitleUpdate:(NSString *)title {
    self.terminalTitle = title;
    if (self.onTitle) self.onTitle(title);
}

- (void)onDisplayLink:(CADisplayLink *)link {
    if (self.metricsActive) {
        [self.displayLinkTimestamps addObject:@(link.timestamp * 1000.0)];
    }
}

- (BOOL)hasMetalLayer:(CALayer *)layer {
    if (!layer) return NO;
    Class ioLayerClass = NSClassFromString(@"IOSurfaceLayer");
    if ([layer isKindOfClass:CAMetalLayer.class] || (ioLayerClass && [layer isKindOfClass:ioLayerClass])) return YES;
    for (CALayer *sub in layer.sublayers) {
        if ([self hasMetalLayer:sub]) return YES;
    }
    return NO;
}

- (void)observeValueForKeyPath:(NSString *)keyPath
                      ofObject:(id)object
                        change:(NSDictionary<NSKeyValueChangeKey, id> *)change
                       context:(void *)context {
    if (context == &gLayerContentsKVOContext) {
        [self recordRenderSubmission];
        return;
    }
    [super observeValueForKeyPath:keyPath ofObject:object change:change context:context];
}

- (void)startRenderMetrics {
    [self stopRenderMetrics];
    self.renderTimestamps = [NSMutableArray array];
    self.displayLinkTimestamps = [NSMutableArray array];
    self.scrollbarOffsetHistory = [NSMutableArray array];
    self.metricsInitialScrolled = self.scrolledRows;
    self.metricsStartMs = monotonicNowMs();
    self.metricsActive = YES;
    if (self.layer) {
        self.observedLayer = self.layer;
        [self.observedLayer addObserver:self
                             forKeyPath:@"contents"
                                options:NSKeyValueObservingOptionNew
                                context:&gLayerContentsKVOContext];
    }
    if (self.window) {
        if (@available(macOS 14.0, *)) {
            self.displayLink = [self displayLinkWithTarget:self selector:@selector(onDisplayLink:)];
            [self.displayLink addToRunLoop:NSRunLoop.mainRunLoop forMode:NSRunLoopCommonModes];
        }
    }
}

- (FlowsTerminalMetricsSummary *)stopRenderMetrics {
    double endMs = monotonicNowMs();
    BOOL wasActive = self.metricsActive;
    self.metricsActive = NO;
    if (self.observedLayer) {
        [self.observedLayer removeObserver:self forKeyPath:@"contents" context:&gLayerContentsKVOContext];
        self.observedLayer = nil;
    }
    if (self.displayLink) {
        [self.displayLink invalidate];
        self.displayLink = nil;
    }
    FlowsTerminalMetricsSummary *summary = [[FlowsTerminalMetricsSummary alloc] init];
    if (!wasActive) return summary;

    summary.durationMs = MAX(0.0, endMs - self.metricsStartMs);
    summary.renderSubmissions = self.renderTimestamps.count;
    summary.submissionRateHz = summary.durationMs > 0 ? (double)summary.renderSubmissions / (summary.durationMs / 1000.0) : 0.0;
    summary.metalLayerAttached = [self hasMetalLayer:self.layer];
    summary.initialScrolledRows = self.metricsInitialScrolled;
    summary.finalScrolledRows = self.scrolledRows;
    summary.scrollbarUpdates = self.scrollbarOffsetHistory.count;
    summary.distinctScrollbarOffsets = [NSSet setWithArray:self.scrollbarOffsetHistory].count;

    NSMutableArray<NSNumber *> *gaps = [NSMutableArray array];
    NSUInteger missed = 0;
    for (NSUInteger i = 1; i < self.renderTimestamps.count; i++) {
        double g = self.renderTimestamps[i].doubleValue - self.renderTimestamps[i - 1].doubleValue;
        [gaps addObject:@(g)];
        if (g > 33.4) missed++;
    }
    [gaps sortUsingSelector:@selector(compare:)];
    summary.missed33_4ms = missed;
    summary.submissionP50GapMs = percentileMs(gaps, 0.50);
    summary.submissionP95GapMs = percentileMs(gaps, 0.95);
    summary.submissionMaxGapMs = gaps.count ? gaps.lastObject.doubleValue : 0.0;

    summary.displayLinkCallbacks = self.displayLinkTimestamps.count;
    summary.displayLinkRateHz = summary.durationMs > 0 ? (double)summary.displayLinkCallbacks / (summary.durationMs / 1000.0) : 0.0;
    NSMutableArray<NSNumber *> *dlGaps = [NSMutableArray array];
    for (NSUInteger i = 1; i < self.displayLinkTimestamps.count; i++) {
        double g = self.displayLinkTimestamps[i].doubleValue - self.displayLinkTimestamps[i - 1].doubleValue;
        [dlGaps addObject:@(g)];
    }
    [dlGaps sortUsingSelector:@selector(compare:)];
    summary.displayLinkP95GapMs = percentileMs(dlGaps, 0.95);
    return summary;
}

- (NSString *)readTextWithPointTag:(ghostty_point_tag_e)tag {
    if (!self.surface) return @"";
    ghostty_selection_s all = {
        .top_left = { .tag = tag, .coord = GHOSTTY_POINT_COORD_TOP_LEFT },
        .bottom_right = { .tag = tag, .coord = GHOSTTY_POINT_COORD_BOTTOM_RIGHT },
    };
    ghostty_text_s data = {0};
    if (!ghostty_surface_read_text(self.surface, all, &data)) return @"";
    NSString *text = [[NSString alloc] initWithBytes:data.text length:data.text_len encoding:NSUTF8StringEncoding];
    ghostty_surface_free_text(self.surface, &data);
    return text ?: @"";
}

- (NSString *)readScreenText {
    return [self readTextWithPointTag:GHOSTTY_POINT_SCREEN];
}

- (NSString *)readViewportText {
    if (!self.surface) return @"";
    ghostty_surface_size_s sz = ghostty_surface_size(self.surface);
    if (sz.columns == 0 || sz.rows == 0) {
        return @"";
    }
    ghostty_selection_s vp = {
        .top_left = { .tag = GHOSTTY_POINT_VIEWPORT, .coord = GHOSTTY_POINT_COORD_EXACT, .x = 0, .y = 0 },
        .bottom_right = { .tag = GHOSTTY_POINT_VIEWPORT, .coord = GHOSTTY_POINT_COORD_EXACT, .x = sz.columns - 1, .y = sz.rows - 1 },
    };
    ghostty_text_s data = {0};
    if (!ghostty_surface_read_text(self.surface, vp, &data)) return @"";
    NSString *text = [[NSString alloc] initWithBytes:data.text length:data.text_len encoding:NSUTF8StringEncoding];
    ghostty_surface_free_text(self.surface, &data);
    return text ?: @"";
}

- (void)keyDown:(NSEvent *)event {
    if (!self.surface) return;
    if ((event.modifierFlags & NSEventModifierFlagCommand) &&
        [[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"w"] &&
        self.onCloseShortcut) {
        self.onCloseShortcut();
        return;
    }
    if ((event.modifierFlags & NSEventModifierFlagCommand) &&
        [[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"c"] &&
        ghostty_surface_has_selection(self.surface)) {
        ghostty_text_s sel = {0};
        if (ghostty_surface_read_selection(self.surface, &sel)) {
            NSString *str = [[NSString alloc] initWithBytes:sel.text length:sel.text_len encoding:NSUTF8StringEncoding];
            if (str) {
                [NSPasteboard.generalPasteboard clearContents];
                [NSPasteboard.generalPasteboard setString:str forType:NSPasteboardTypeString];
            }
            ghostty_surface_free_text(self.surface, &sel);
        }
        return;
    }
    if (!self.inputOwner) return;
    if ((event.modifierFlags & NSEventModifierFlagCommand) &&
        [[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"v"]) {
        [self paste:nil];
        return;
    }
    NSString *text = event.characters;
    BOOL hardwareInput = (event.modifierFlags & (NSEventModifierFlagControl | NSEventModifierFlagCommand)) != 0;
    if (!hardwareInput) {
        BOOL wasComposing = self.hasMarkedText;
        self.interpreting = YES;
        self.commit = nil;
        [self interpretKeyEvents:@[event]];
        self.interpreting = NO;
        if (wasComposing || self.hasMarkedText) {
            if (self.commit.length > 0) {
                ghostty_surface_text(self.surface, self.commit.UTF8String, [self.commit lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
            }
            return;
        }
        if (self.commit.length > 0) text = self.commit;
    }
    if (text.length == 1 && [[NSCharacterSet controlCharacterSet] characterIsMember:[text characterAtIndex:0]]) {
        text = nil;
    }
    NSString *unshifted = [event charactersByApplyingModifiers:0] ?: event.charactersIgnoringModifiers;
    ghostty_input_key_s key = {
        .action = event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS,
        .mods = inputMods(event),
        .consumed_mods = inputMods(event) & ~(GHOSTTY_MODS_CTRL | GHOSTTY_MODS_SUPER),
        .keycode = event.keyCode,
        .text = text.UTF8String,
        .unshifted_codepoint = unshifted.length > 0 ? [unshifted characterAtIndex:0] : 0,
    };
    ghostty_surface_key(self.surface, key);
}

- (void)keyUp:(NSEvent *)event {
    if (!self.surface || !self.inputOwner) return;
    ghostty_input_key_s key = {
        .action = GHOSTTY_ACTION_RELEASE,
        .mods = inputMods(event),
        .keycode = event.keyCode,
    };
    ghostty_surface_key(self.surface, key);
}

- (void)paste:(id)sender {
    (void)sender;
    if (!self.surface || !self.inputOwner) return;
    NSString *text = [NSPasteboard.generalPasteboard stringForType:NSPasteboardTypeString];
    if (text.length != 0) {
        ghostty_surface_paste_text(self.surface, text.UTF8String, [text lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
    }
}

- (void)updatePointer:(NSEvent *)event {
    if (!self.surface) return;
    NSPoint point = [self convertPoint:event.locationInWindow fromView:nil];
    ghostty_surface_mouse_pos(self.surface, point.x, point.y, inputMods(event));
}

- (void)mouseDown:(NSEvent *)event {
    [self.window makeFirstResponder:self];
    if (!self.surface || !self.inputOwner) return;
    [self updatePointer:event];
    ghostty_surface_mouse_button(self.surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, inputMods(event));
}

- (void)mouseUp:(NSEvent *)event {
    if (!self.surface || !self.inputOwner) return;
    [self updatePointer:event];
    ghostty_surface_mouse_button(self.surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, inputMods(event));
}

- (void)mouseDragged:(NSEvent *)event {
    if (!self.surface || !self.inputOwner) return;
    [self updatePointer:event];
}

- (void)scrollWheel:(NSEvent *)event {
    if (!self.surface) return;
    if (!self.inputOwner && ghostty_surface_mouse_captured(self.surface)) {
        return;
    }
    [self updatePointer:event];
    ghostty_surface_mouse_scroll(self.surface, event.scrollingDeltaX, event.scrollingDeltaY, scrollMods(event));
}

- (void)insertText:(id)value replacementRange:(NSRange)range {
    (void)range;
    if (!self.surface || !self.inputOwner) return;
    NSString *text = [value isKindOfClass:NSAttributedString.class] ? [value string] : value;
    [self unmarkText];
    if (self.interpreting) {
        self.commit = self.commit ? [self.commit stringByAppendingString:text] : text;
    } else {
        ghostty_surface_text(self.surface, text.UTF8String, [text lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
    }
}

- (void)setMarkedText:(id)value selectedRange:(NSRange)selected replacementRange:(NSRange)range {
    (void)selected; (void)range;
    if (!self.surface || !self.inputOwner) return;
    self.marked = [value isKindOfClass:NSAttributedString.class] ? [value string] : value;
    ghostty_surface_preedit(self.surface, self.marked.UTF8String, [self.marked lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
}

- (void)unmarkText {
    self.marked = nil;
    if (self.surface) ghostty_surface_preedit(self.surface, NULL, 0);
}

- (BOOL)hasMarkedText { return self.marked.length > 0; }
- (NSRange)markedRange { return self.hasMarkedText ? NSMakeRange(0, self.marked.length) : NSMakeRange(NSNotFound, 0); }
- (NSRange)selectedRange { return NSMakeRange(NSNotFound, 0); }
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText { return @[]; }
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actualRange { (void)range; (void)actualRange; return nil; }
- (NSUInteger)characterIndexForPoint:(NSPoint)point { (void)point; return NSNotFound; }
- (void)doCommandBySelector:(SEL)selector { (void)selector; }

- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actualRange {
    (void)range; (void)actualRange;
    double x = 0, y = 0, width = 0, height = 0;
    if (self.surface) ghostty_surface_ime_point(self.surface, &x, &y, &width, &height);
    NSRect windowRect = [self convertRect:NSMakeRect(x, y, width, height) toView:nil];
    return [self.window convertRectToScreen:windowRect];
}
@end

#pragma clang diagnostic pop
