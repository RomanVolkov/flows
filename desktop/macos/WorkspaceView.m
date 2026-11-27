#import <AppKit/AppKit.h>
#import <CoreText/CoreText.h>
#import "TerminalView.h"

NS_ASSUME_NONNULL_BEGIN

static NSColor *flowsHexColor(NSString *hex) {
    NSString *clean = [hex hasPrefix:@"#"] ? [hex substringFromIndex:1] : hex;
    unsigned int rgb = 0;
    [[NSScanner scannerWithString:clean] scanHexInt:&rgb];
    return [NSColor colorWithSRGBRed:((rgb >> 16) & 0xFF) / 255.0
                               green:((rgb >> 8) & 0xFF) / 255.0
                                blue:(rgb & 0xFF) / 255.0
                               alpha:1.0];
}

static NSFont *flowsMonoFont(CGFloat size) {
    NSFont *font = [NSFont fontWithName:@"Iosevka Mono" size:size];
    if (!font) {
        font = [NSFont monospacedSystemFontOfSize:size weight:NSFontWeightRegular];
    }
    return font;
}

@interface FlowsPaneView : NSView
@property(nonatomic, copy) NSString *paneName;
@property(nonatomic, strong) NSColor *fillColor;
@end

@implementation FlowsPaneView
- (BOOL)acceptsFirstResponder { return YES; }
- (void)drawRect:(NSRect)dirtyRect {
    [self.fillColor ?: flowsHexColor(@"#0A0A0B") setFill];
    NSRectFill(dirtyRect);
}
@end

@interface FlowsWorkspaceController : NSObject <NSWindowDelegate>
@property(nonatomic, strong) NSWindow *window;
@property(nonatomic, strong) NSView *topBar;
@property(nonatomic, strong) NSTextField *reconnectBanner;
@property(nonatomic, strong) NSButton *detachButton;
@property(nonatomic, strong) FlowsPaneView *sidebarView;
@property(nonatomic, strong) FlowsPaneView *canvasView;
@property(nonatomic, strong) FlowsPaneView *inspectorView;
@property(nonatomic, strong) FlowsPaneView *terminalDockView;
@property(nonatomic, strong) TerminalView *terminalView;
@property(nonatomic, strong) NSTextField *selectionSummaryLabel;
@property(nonatomic, strong) NSTextField *footerLabel;
@property(nonatomic) BOOL dockOpen;
@property(nonatomic) BOOL inputOwner;
@property(nonatomic, copy, nullable) NSString *activeSessionID;
- (void)attachSession:(NSString *)sessionID;
- (BOOL)attachSession:(NSString *)sessionID
           executable:(NSString *)executable
           socketPath:(NSString *)socketPath
                nonce:(nullable NSString *)nonce;
- (void)detachDock;
- (void)cycleFocus;
- (BOOL)handleKeyEvent:(NSEvent *)event;
@end

@implementation FlowsWorkspaceController

- (instancetype)init {
    self = [super init];
    if (!self) return nil;

    NSRect frame = NSMakeRect(0, 0, 1200, 760);
    _window = [[NSWindow alloc] initWithContentRect:frame
                                          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
                                            backing:NSBackingStoreBuffered
                                              defer:NO];
    _window.delegate = self;
    _window.title = @"Flows";
    _window.backgroundColor = flowsHexColor(@"#0A0A0B");

    NSView *root = _window.contentView;
    root.wantsLayer = YES;
    root.layer.backgroundColor = flowsHexColor(@"#0A0A0B").CGColor;

    _topBar = [[NSView alloc] initWithFrame:NSMakeRect(0, 720, 1200, 40)];
    _topBar.wantsLayer = YES;
    _topBar.layer.backgroundColor = flowsHexColor(@"#111113").CGColor;

    _reconnectBanner = [NSTextField labelWithString:@"Reconnecting..."];
    _reconnectBanner.font = flowsMonoFont(12);
    _reconnectBanner.textColor = flowsHexColor(@"#F59E0B");
    _reconnectBanner.frame = NSMakeRect(16, 10, 240, 20);
    _reconnectBanner.hidden = YES;
    [_topBar addSubview:_reconnectBanner];

    _detachButton = [NSButton buttonWithTitle:@"Detach Terminal" target:self action:@selector(detachDock)];
    _detachButton.font = flowsMonoFont(12);
    _detachButton.frame = NSMakeRect(1040, 6, 140, 28);
    _detachButton.hidden = YES;
    [_topBar addSubview:_detachButton];
    [root addSubview:_topBar];

    _sidebarView = [[FlowsPaneView alloc] initWithFrame:NSMakeRect(0, 28, 220, 692)];
    _sidebarView.paneName = @"sidebar";
    _sidebarView.fillColor = flowsHexColor(@"#111113");
    [root addSubview:_sidebarView];

    _canvasView = [[FlowsPaneView alloc] initWithFrame:NSMakeRect(220, 28, 720, 692)];
    _canvasView.paneName = @"canvas";
    _canvasView.fillColor = flowsHexColor(@"#0A0A0B");
    [root addSubview:_canvasView];

    _inspectorView = [[FlowsPaneView alloc] initWithFrame:NSMakeRect(940, 28, 260, 692)];
    _inspectorView.paneName = @"inspector";
    _inspectorView.fillColor = flowsHexColor(@"#18181B");
    _selectionSummaryLabel = [NSTextField labelWithString:@"No selection"];
    _selectionSummaryLabel.font = flowsMonoFont(12);
    _selectionSummaryLabel.textColor = flowsHexColor(@"#FAFAFA");
    _selectionSummaryLabel.frame = NSMakeRect(12, 650, 236, 24);
    [_inspectorView addSubview:_selectionSummaryLabel];
    [root addSubview:_inspectorView];

    _terminalDockView = [[FlowsPaneView alloc] initWithFrame:NSMakeRect(580, 28, 360, 692)];
    _terminalDockView.paneName = @"terminal";
    _terminalDockView.fillColor = flowsHexColor(@"#101019");
    _terminalDockView.autoresizesSubviews = YES;
    _terminalDockView.hidden = YES;
    _terminalView = [[TerminalView alloc] initWithFrame:_terminalDockView.bounds];
    _terminalView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    [_terminalDockView addSubview:_terminalView];
    [root addSubview:_terminalDockView];

    _footerLabel = [NSTextField labelWithString:@"Ready"];
    _footerLabel.font = flowsMonoFont(11);
    _footerLabel.textColor = flowsHexColor(@"#A1A1AA");
    _footerLabel.frame = NSMakeRect(12, 4, 400, 20);
    [root addSubview:_footerLabel];

    [_window makeFirstResponder:_canvasView];
    return self;
}

- (void)attachSession:(NSString *)sessionID {
    [self.terminalView detach];
    self.activeSessionID = sessionID;
    self.dockOpen = sessionID.length > 0;
    self.inputOwner = self.dockOpen;
    self.terminalDockView.hidden = !self.dockOpen;
    self.detachButton.hidden = !self.dockOpen;
    if (self.dockOpen) {
        self.canvasView.frame = NSMakeRect(220, 28, 360, 692);
        [self.window makeFirstResponder:self.terminalDockView];
    } else {
        self.canvasView.frame = NSMakeRect(220, 28, 720, 692);
        [self.window makeFirstResponder:self.canvasView];
    }
}

- (BOOL)attachSession:(NSString *)sessionID
           executable:(NSString *)executable
           socketPath:(NSString *)socketPath
                nonce:(nullable NSString *)nonce {
    if (sessionID.length == 0 || executable.length == 0 || socketPath.length == 0) {
        return NO;
    }
    __weak typeof(self) weakSelf = self;
    self.terminalView.onInputOwnerChanged = ^(BOOL isOwner) {
        weakSelf.inputOwner = isOwner;
    };
    self.terminalView.onCloseShortcut = ^{
        [weakSelf detachDock];
    };
    self.terminalView.onExit = ^(int code) {
        (void)code;
        [weakSelf detachDock];
    };
    self.activeSessionID = sessionID;
    self.dockOpen = YES;
    self.terminalDockView.hidden = NO;
    self.detachButton.hidden = NO;
    self.canvasView.frame = NSMakeRect(220, 28, 360, 692);
    self.terminalDockView.frame = NSMakeRect(580, 28, 360, 692);
    BOOL ok = [self.terminalView attachWithExecutable:executable
                                           socketPath:socketPath
                                            sessionID:sessionID
                                             clientID:nil
                                                nonce:nonce
                                                epoch:0
                                             fontSize:19.0f];
    if (!ok) {
        [self detachDock];
        return NO;
    }
    self.inputOwner = self.terminalView.inputOwner;
    [self.window makeFirstResponder:self.terminalView];
    return YES;
}

- (void)detachDock {
    [self.terminalView detach];
    self.dockOpen = NO;
    self.inputOwner = NO;
    self.terminalDockView.hidden = YES;
    self.detachButton.hidden = YES;
    self.canvasView.frame = NSMakeRect(220, 28, 720, 692);
    [self.window makeFirstResponder:self.canvasView];
}

- (void)cycleFocus {
    NSView *terminalFocusTarget = self.terminalView.surface ? (NSView *)self.terminalView : self.terminalDockView;
    NSArray<NSView *> *order = self.dockOpen
        ? @[self.sidebarView, self.canvasView, self.inspectorView, terminalFocusTarget]
        : @[self.sidebarView, self.canvasView, self.inspectorView];
    NSUInteger idx = [order indexOfObject:(NSView *)self.window.firstResponder];
    NSUInteger next = (idx == NSNotFound) ? 0 : (idx + 1) % order.count;
    [self.window makeFirstResponder:order[next]];
}

- (BOOL)handleKeyEvent:(NSEvent *)event {
    if ((event.modifierFlags & NSEventModifierFlagCommand) &&
        [[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"w"]) {
        if (self.dockOpen) {
            [self detachDock];
            return YES;
        }
    }
    BOOL terminalFocused = (self.window.firstResponder == self.terminalDockView ||
                            self.window.firstResponder == self.terminalView);
    if (event.keyCode == 53 && terminalFocused) {
        // Escape goes to the harness when terminal has focus.
        return YES;
    }
    return NO;
}

- (void)windowWillClose:(NSNotification *)notification {
    (void)notification;
    // Closing the window releases input ownership while leaving the underlying session host alive.
    [self.terminalView detach];
    self.inputOwner = NO;
}

@end

NS_ASSUME_NONNULL_END
