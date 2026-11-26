#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#include <ghostty.h>

void flowsVerifyTerminal(NSWindow *, ghostty_surface_t, NSString *, NSString *, int *);
static int runStatus;

@interface TerminalView : NSView <NSTextInputClient>
@property(nonatomic) ghostty_surface_t surface;
@property(nonatomic, copy) NSString *marked;
@property(nonatomic) BOOL interpreting;
@property(nonatomic, copy) NSString *commit;
- (void)syncSize;
@end

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

@implementation TerminalView
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
    double scale = self.window.backingScaleFactor;
    NSNumber *display = self.window.screen.deviceDescription[@"NSScreenNumber"];
    if (display) ghostty_surface_set_display_id(self.surface, display.unsignedIntValue);
    ghostty_surface_set_content_scale(self.surface, scale, scale);
    ghostty_surface_set_size(self.surface, (uint32_t)pixels.width, (uint32_t)pixels.height);
}

- (void)keyDown:(NSEvent *)event {
    if ((event.modifierFlags & NSEventModifierFlagCommand) && [event.charactersIgnoringModifiers isEqualToString:@"v"]) {
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
    ghostty_input_key_s key = {
        .action = GHOSTTY_ACTION_RELEASE,
        .mods = inputMods(event),
        .keycode = event.keyCode,
    };
    ghostty_surface_key(self.surface, key);
}

- (void)paste:(id)sender {
    NSString *text = [NSPasteboard.generalPasteboard stringForType:NSPasteboardTypeString];
    if (text.length != 0) {
        ghostty_surface_paste_text(self.surface, text.UTF8String, [text lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
    }
}

- (void)updatePointer:(NSEvent *)event {
    NSPoint point = [self convertPoint:event.locationInWindow fromView:nil];
    ghostty_surface_mouse_pos(self.surface, point.x, point.y, inputMods(event));
}

- (void)mouseDown:(NSEvent *)event {
    [self.window makeFirstResponder:self];
    [self updatePointer:event];
    ghostty_surface_mouse_button(self.surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, inputMods(event));
}

- (void)mouseUp:(NSEvent *)event {
    [self updatePointer:event];
    ghostty_surface_mouse_button(self.surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, inputMods(event));
}

- (void)mouseDragged:(NSEvent *)event { [self updatePointer:event]; }

- (void)scrollWheel:(NSEvent *)event {
    [self updatePointer:event];
    ghostty_surface_mouse_scroll(self.surface, event.scrollingDeltaX, event.scrollingDeltaY, event.hasPreciseScrollingDeltas);
}

- (void)insertText:(id)value replacementRange:(NSRange)range {
    NSString *text = [value isKindOfClass:NSAttributedString.class] ? [value string] : value;
    [self unmarkText];
    if (self.interpreting) {
        self.commit = self.commit ? [self.commit stringByAppendingString:text] : text;
    } else {
        ghostty_surface_text(self.surface, text.UTF8String, [text lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
    }
}

- (void)setMarkedText:(id)value selectedRange:(NSRange)selected replacementRange:(NSRange)range {
    self.marked = [value isKindOfClass:NSAttributedString.class] ? [value string] : value;
    ghostty_surface_preedit(self.surface, self.marked.UTF8String, [self.marked lengthOfBytesUsingEncoding:NSUTF8StringEncoding]);
}

- (void)unmarkText {
    self.marked = nil;
    ghostty_surface_preedit(self.surface, NULL, 0);
}

- (BOOL)hasMarkedText { return self.marked.length > 0; }
- (NSRange)markedRange { return self.hasMarkedText ? NSMakeRange(0, self.marked.length) : NSMakeRange(NSNotFound, 0); }
- (NSRange)selectedRange { return NSMakeRange(NSNotFound, 0); }
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText { return @[]; }
- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range actualRange:(NSRangePointer)actualRange { return nil; }
- (NSUInteger)characterIndexForPoint:(NSPoint)point { return NSNotFound; }
- (void)doCommandBySelector:(SEL)selector {}

- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actualRange {
    double x, y, width, height;
    ghostty_surface_ime_point(self.surface, &x, &y, &width, &height);
    NSRect windowRect = [self convertRect:NSMakeRect(x, y, width, height) toView:nil];
    return [self.window convertRectToScreen:windowRect];
}
@end

@interface ProbeApp : NSObject <NSApplicationDelegate, NSWindowDelegate>
@property(nonatomic) ghostty_app_t app;
@property(nonatomic) NSWindow *window;
@property(nonatomic) TerminalView *terminal;
@property(nonatomic, copy) NSString *command;
@end

static void wakeup(void *context) {
    ProbeApp *delegate = (__bridge ProbeApp *)context;
    dispatch_async(dispatch_get_main_queue(), ^{
        if (delegate.app) ghostty_app_tick(delegate.app);
    });
}

static bool action(ghostty_app_t app, ghostty_target_s target, ghostty_action_s action) {
    if (target.tag == GHOSTTY_TARGET_SURFACE && action.tag == GHOSTTY_ACTION_RENDER) {
        ghostty_surface_draw(target.target.surface);
        return true;
    }
    return false;
}

static ghostty_clipboard_read_result_e readClipboard(void *context, ghostty_clipboard_e clipboard,
    void *request, const char *const *types, size_t count, bool confirmed) {
    return GHOSTTY_CLIPBOARD_READ_UNSUPPORTED;
}

static void confirmClipboard(void *context, const ghostty_clipboard_confirm_s *data,
    void *request, ghostty_clipboard_request_e kind) {
    TerminalView *view = (__bridge TerminalView *)context;
    if (view.surface) ghostty_surface_deny_clipboard_request(view.surface, request);
}

static void writeClipboard(void *context, ghostty_clipboard_e clipboard,
    const ghostty_clipboard_content_s *data, size_t count, bool confirmed) {}


@implementation ProbeApp
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    ghostty_config_t config = ghostty_config_new();
    ghostty_config_finalize(config);
    ghostty_runtime_config_s runtime = {
        .userdata = (__bridge void *)self,
        .wakeup_cb = wakeup,
        .action_cb = action,
        .read_clipboard_cb = readClipboard,
        .confirm_read_clipboard_cb = confirmClipboard,
        .write_clipboard_cb = writeClipboard,
    };
    self.app = ghostty_app_new(&runtime, config);
    ghostty_config_free(config);
    if (!self.app) {
        fprintf(stderr, "Could not initialize the native Ghostty application\n");
        exit(1);
    }
    NSString *phase = NSProcessInfo.processInfo.environment[@"FLOWS_TERMINAL_PHASE"];
    NSRect contentRect = [phase isEqualToString:@"2"] ? NSMakeRect(0, 0, 700, 420) : NSMakeRect(0, 0, 800, 480);
    self.window = [[NSWindow alloc] initWithContentRect:contentRect
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    self.window.title = @"Flows Terminal";
    self.window.delegate = self;
    self.terminal = [[TerminalView alloc] initWithFrame:contentRect];
    self.window.contentView = self.terminal;
    ghostty_surface_config_s surface = ghostty_surface_config_new();
    surface.platform_tag = GHOSTTY_PLATFORM_MACOS;
    surface.platform.macos.nsview = (__bridge void *)self.terminal;
    surface.userdata = (__bridge void *)self.terminal;
    surface.scale_factor = self.window.backingScaleFactor;
    surface.command = self.command.UTF8String;
    surface.wait_after_command = true;
    self.terminal.surface = ghostty_surface_new(self.app, &surface);
    if (!self.terminal.surface) {
        fprintf(stderr, "Could not initialize the native Ghostty surface\n");
        exit(1);
    }
    [self.terminal syncSize];
    [self.window center];
    [self.window makeKeyAndOrderFront:nil];
    [self.window makeFirstResponder:self.terminal];
    [NSApp activateIgnoringOtherApps:YES];
    NSString *marker = NSProcessInfo.processInfo.environment[@"FLOWS_TERMINAL_EXPECT"];
    if (marker.length) flowsVerifyTerminal(self.window, self.terminal.surface, marker, phase, &runStatus);
}

- (void)windowWillClose:(NSNotification *)notification {
    [NSApp stop:nil];
    NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
        location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0
        context:nil subtype:0 data1:0 data2:0];
    [NSApp postEvent:event atStart:NO];
}

- (void)disconnect {
    if (self.terminal.surface) {
        ghostty_surface_free(self.terminal.surface);
        self.terminal.surface = NULL;
    }
    if (self.app) {
        ghostty_app_free(self.app);
        self.app = NULL;
    }
}
@end

int main(int argc, char **argv) {
    if (argc != 2 || ghostty_init(0, NULL) != 0) return 2;
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        ProbeApp *delegate = [ProbeApp new];
        delegate.command = [NSString stringWithUTF8String:argv[1]];
        app.delegate = delegate;
        [app run];
        [delegate disconnect];
    }
    return runStatus;
}
