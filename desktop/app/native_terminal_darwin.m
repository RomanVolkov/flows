//go:build desktop && production && darwin

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wnullability-completeness"
#import <AppKit/AppKit.h>
#import "../macos/TerminalView.m"

extern void flowsNativeTerminalDispatchEvent(
    const char *id,
    const char *eventType,
    const char *ownerID,
    const char *reason,
    const char *title,
    uint64_t scrolled,
    int exitCode,
    bool isOwner
);

static NSMutableDictionary<NSString *, TerminalView *> *flowsNativeViews(void) {
    static NSMutableDictionary<NSString *, TerminalView *> *views = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        views = [NSMutableDictionary dictionary];
    });
    return views;
}

static void runOnMainSync(dispatch_block_t block) {
    if (NSThread.isMainThread) {
        block();
    } else {
        dispatch_sync(dispatch_get_main_queue(), block);
    }
}

static NSWindow *flowsTargetWindow(void) {
    return NSApp.keyWindow ?: NSApp.mainWindow ?: NSApp.windows.firstObject;
}

static NSRect flowsContentFrame(NSView *contentView, double x, double y, double width, double height) {
    CGFloat w = MAX(1.0, width);
    CGFloat h = MAX(1.0, height);
    CGFloat frameY = contentView.isFlipped ? y : (contentView.bounds.size.height - y - h);
    return NSMakeRect(x, frameY, w, h);
}

bool flowsNativeTerminalOpen(const char *cid, const char *cexe, const char *csocketPath, const char *csessionID, const char *cnonce, double x, double y, double width, double height, float fontSize) {
    NSString *viewID = [NSString stringWithUTF8String:cid];
    NSString *exe = [NSString stringWithUTF8String:cexe];
    NSString *socketPath = [NSString stringWithUTF8String:csocketPath];
    NSString *sessionID = [NSString stringWithUTF8String:csessionID];
    NSString *nonce = (cnonce && cnonce[0] != '\0') ? [NSString stringWithUTF8String:cnonce] : nil;

    __block BOOL opened = NO;
    runOnMainSync(^{
        NSWindow *window = flowsTargetWindow();
        NSView *contentView = window.contentView;
        if (!window || !contentView) return;

        TerminalView *existing = flowsNativeViews()[viewID];
        if (existing) {
            [existing detach];
            [existing removeFromSuperview];
            [flowsNativeViews() removeObjectForKey:viewID];
        }

        NSRect frame = flowsContentFrame(contentView, x, y, width, height);
        TerminalView *view = [[TerminalView alloc] initWithFrame:frame];
        view.wantsLayer = YES;

        __weak TerminalView *weakView = view;
        view.onReady = ^{
            TerminalView *v = weakView;
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "ready", NULL, NULL, NULL, v ? v.initialScrolledRows : 0, 0, v ? v.inputOwner : false);
        };
        view.onInputOwnerChanged = ^(BOOL isOwner) {
            TerminalView *v = weakView;
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "input_owner", v.ownerID.UTF8String, NULL, NULL, v ? v.scrolledRows : 0, 0, isOwner);
        };
        view.onScrollbar = ^(uint64_t total, uint64_t offset, uint64_t len) {
            uint64_t scrolled = total > offset + len ? total - offset - len : 0;
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "scrollbar", NULL, NULL, NULL, scrolled, 0, false);
        };
        view.onTitle = ^(NSString *title) {
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "title", NULL, NULL, title.UTF8String, 0, 0, false);
        };
        view.onFenced = ^(NSString *reason) {
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "fenced", NULL, reason.UTF8String, NULL, 0, 0, false);
        };
        view.onExit = ^(int code) {
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "exit", NULL, NULL, NULL, 0, code, false);
        };
        view.onCloseShortcut = ^{
            flowsNativeTerminalDispatchEvent(viewID.UTF8String, "close_requested", NULL, NULL, NULL, 0, 0, false);
        };

        [contentView addSubview:view positioned:NSWindowAbove relativeTo:nil];
        if (![view attachWithExecutable:exe
                             subcommand:@"--native-attach"
                             socketPath:socketPath
                              sessionID:sessionID
                               clientID:viewID
                                  nonce:nonce
                                  epoch:0
                               fontSize:fontSize]) {
            [view removeFromSuperview];
            return;
        }
        flowsNativeViews()[viewID] = view;
        [window makeFirstResponder:view];
        opened = YES;
    });
    return opened;
}

bool flowsNativeTerminalSetBounds(const char *cid, double x, double y, double width, double height) {
    NSString *viewID = [NSString stringWithUTF8String:cid];
    __block BOOL updated = NO;
    runOnMainSync(^{
        TerminalView *view = flowsNativeViews()[viewID];
        NSView *contentView = view.superview;
        if (!view || !contentView) return;
        view.frame = flowsContentFrame(contentView, x, y, width, height);
        [view syncSize];
        updated = YES;
    });
    return updated;
}

bool flowsNativeTerminalFocus(const char *cid) {
    NSString *viewID = [NSString stringWithUTF8String:cid];
    __block BOOL focused = NO;
    runOnMainSync(^{
        TerminalView *view = flowsNativeViews()[viewID];
        if (!view || !view.window) return;
        [view.window makeFirstResponder:view];
        focused = YES;
    });
    return focused;
}

bool flowsNativeTerminalClaimInput(const char *cid) {
    NSString *viewID = [NSString stringWithUTF8String:cid];
    __block BOOL claimed = NO;
    runOnMainSync(^{
        TerminalView *view = flowsNativeViews()[viewID];
        if (!view) return;
        [view claimInput];
        claimed = YES;
    });
    return claimed;
}

void flowsNativeTerminalClose(const char *cid) {
    NSString *viewID = [NSString stringWithUTF8String:cid];
    runOnMainSync(^{
        TerminalView *view = flowsNativeViews()[viewID];
        if (!view) return;
        NSWindow *window = view.window;
        [view detach];
        [view removeFromSuperview];
        [flowsNativeViews() removeObjectForKey:viewID];
        if (window && window.contentView) {
            [window makeFirstResponder:window.contentView];
        }
    });
}

#pragma clang diagnostic pop
