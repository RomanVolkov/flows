#import <AppKit/AppKit.h>
#include <ghostty.h>

static NSString *readScreen(ghostty_surface_t surface) {
    ghostty_selection_s all = {
        .top_left = { .tag = GHOSTTY_POINT_SCREEN, .coord = GHOSTTY_POINT_COORD_TOP_LEFT },
        .bottom_right = { .tag = GHOSTTY_POINT_SCREEN, .coord = GHOSTTY_POINT_COORD_BOTTOM_RIGHT },
    };
    ghostty_text_s data = {0};
    if (!ghostty_surface_read_text(surface, all, &data)) return @"";
    NSString *text = [[NSString alloc] initWithBytes:data.text length:data.text_len encoding:NSUTF8StringEncoding];
    ghostty_surface_free_text(surface, &data);
    return text ?: @"";
}

void flowsVerifyTerminal(NSWindow *window, ghostty_surface_t surface, NSString *marker, NSString *phase, int *result) {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
    __block BOOL sent = NO;
    __block BOOL resized = NO;
    [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer) {
        NSString *text = readScreen(surface);
        if (!sent && [text containsString:marker] && [text containsString:@"cursor-ok"]) {
            sent = YES;
            id<NSTextInputClient> client = (id<NSTextInputClient>)window.firstResponder;
            NSRange unset = NSMakeRange(NSNotFound, 0);
            [client setMarkedText:@"雪" selectedRange:NSMakeRange(0, 1) replacementRange:unset];
            if (!client.hasMarkedText) {
                *result = 1;
                [timer invalidate];
                [window close];
                return;
            }
            NSString *input = [NSString stringWithFormat:@"typed-雪-%@\n", phase];
            [client insertText:input replacementRange:unset];
            NSEvent *interrupt = [NSEvent keyEventWithType:NSEventTypeKeyDown
                location:NSZeroPoint modifierFlags:NSEventModifierFlagControl
                timestamp:0 windowNumber:window.windowNumber context:nil
                characters:@"\003" charactersIgnoringModifiers:@"c" isARepeat:NO keyCode:8];
            [window.firstResponder keyDown:interrupt];
            ghostty_surface_paste_text(surface, "paste", 5);
            ghostty_surface_mouse_pos(surface, 60, 100, GHOSTTY_MODS_NONE);
            ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT, GHOSTTY_MODS_NONE);
            ghostty_surface_mouse_button(surface, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT, GHOSTTY_MODS_NONE);
        }
        BOOL inputComplete = sent;
        for (NSString *item in @[@"input", @"control", @"paste", @"mouse"]) {
            inputComplete &= [text containsString:[NSString stringWithFormat:@"%@-ok-%@", item, phase]];
        }
        if (inputComplete && !resized) {
            resized = YES;
            NSSize size = [phase isEqualToString:@"1"] ? NSMakeSize(700, 420) : NSMakeSize(760, 440);
            [window setContentSize:size];
        }
        BOOL complete = resized && [text containsString:[NSString stringWithFormat:@"resize-ok-%@", phase]];
        if (complete) {
            fprintf(stdout, "Verified attachment %s to %s\n", phase.UTF8String, marker.UTF8String);
            [timer invalidate];
            [window close];
        } else if (deadline.timeIntervalSinceNow <= 0) {
            fprintf(stderr, "Terminal fixture timed out waiting for %s. Screen: %s\n", marker.UTF8String, text.UTF8String);
            *result = 1;
            [timer invalidate];
            [window close];
        }
    }];
}
