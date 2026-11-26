#import <AppKit/AppKit.h>
#include <math.h>

@interface FlowsCanvasGestureView : NSView
@property(nonatomic) NSPoint panOffset;
@property(nonatomic) CGFloat zoomScale;
@property(nonatomic) BOOL hasManualNav;
@property(nonatomic) BOOL spaceHeld;
@property(nonatomic) NSRect worldBounds;
- (void)fitWorldWithMargin:(CGFloat)margin manual:(BOOL)manual;
- (void)zoomAtViewPoint:(NSPoint)pt factor:(CGFloat)factor;
@end

@implementation FlowsCanvasGestureView

- (instancetype)initWithFrame:(NSRect)frameRect {
    self = [super initWithFrame:frameRect];
    if (self) {
        _zoomScale = 1.0;
    }
    return self;
}

- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstResponder { return YES; }

- (CGFloat)clampZoom:(CGFloat)z {
    if (z < 0.2) return 0.2;
    if (z > 3.0) return 3.0;
    return z;
}

- (void)zoomAtViewPoint:(NSPoint)pt factor:(CGFloat)factor {
    CGFloat oldZoom = self.zoomScale > 0 ? self.zoomScale : 1.0;
    CGFloat worldX = (pt.x - self.panOffset.x) / oldZoom;
    CGFloat worldY = (pt.y - self.panOffset.y) / oldZoom;
    CGFloat newZoom = [self clampZoom:oldZoom * factor];
    self.zoomScale = newZoom;
    self.panOffset = NSMakePoint(pt.x - worldX * newZoom, pt.y - worldY * newZoom);
    self.hasManualNav = YES;
    [self setNeedsDisplay:YES];
}

- (void)fitWorldWithMargin:(CGFloat)margin manual:(BOOL)manual {
    if (self.worldBounds.size.width <= 0 || self.worldBounds.size.height <= 0 ||
        self.bounds.size.width <= 0 || self.bounds.size.height <= 0) {
        self.panOffset = NSZeroPoint;
        self.zoomScale = 1.0;
        if (manual) self.hasManualNav = YES;
        [self setNeedsDisplay:YES];
        return;
    }
    CGFloat availW = fmax(1.0, self.bounds.size.width - 2.0 * margin);
    CGFloat availH = fmax(1.0, self.bounds.size.height - 2.0 * margin);
    CGFloat z = fmin(availW / self.worldBounds.size.width, availH / self.worldBounds.size.height);
    if (z > 1.0) z = 1.0;
    if (z < 0.2) z = 0.2;
    self.zoomScale = z;
    CGFloat cx = NSMidX(self.worldBounds);
    CGFloat cy = NSMidY(self.worldBounds);
    self.panOffset = NSMakePoint(self.bounds.size.width / 2.0 - cx * z, self.bounds.size.height / 2.0 - cy * z);
    if (manual) self.hasManualNav = YES;
    [self setNeedsDisplay:YES];
}

- (void)scrollWheel:(NSEvent *)event {
    BOOL zoomMod = (event.modifierFlags & (NSEventModifierFlagCommand | NSEventModifierFlagControl)) != 0;
    if (zoomMod) {
        NSPoint pt = [self convertPoint:event.locationInWindow fromView:nil];
        CGFloat factor = 1.0 + event.scrollingDeltaY * 0.01;
        if (factor < 0.5) factor = 0.5;
        if (factor > 1.5) factor = 1.5;
        [self zoomAtViewPoint:pt factor:factor];
        return;
    }
    self.panOffset = NSMakePoint(self.panOffset.x + event.scrollingDeltaX, self.panOffset.y + event.scrollingDeltaY);
    self.hasManualNav = YES;
    [self setNeedsDisplay:YES];
}

- (void)magnifyWithEvent:(NSEvent *)event {
    NSPoint pt = [self convertPoint:event.locationInWindow fromView:nil];
    [self zoomAtViewPoint:pt factor:(1.0 + event.magnification)];
}

- (void)otherMouseDragged:(NSEvent *)event {
    // Middle-drag pans the canvas.
    self.panOffset = NSMakePoint(self.panOffset.x + event.deltaX, self.panOffset.y + event.deltaY);
    self.hasManualNav = YES;
    [self setNeedsDisplay:YES];
}

- (void)mouseDragged:(NSEvent *)event {
    if (self.spaceHeld) {
        self.panOffset = NSMakePoint(self.panOffset.x + event.deltaX, self.panOffset.y + event.deltaY);
        self.hasManualNav = YES;
        [self setNeedsDisplay:YES];
        return;
    }
    [super mouseDragged:event];
}

- (void)keyDown:(NSEvent *)event {
    if ([event.charactersIgnoringModifiers isEqualToString:@" "]) {
        self.spaceHeld = YES;
        return;
    }
    if ([[event.charactersIgnoringModifiers lowercaseString] isEqualToString:@"f"]) {
        [self fitWorldWithMargin:40.0 manual:YES];
        return;
    }
    [super keyDown:event];
}

- (void)keyUp:(NSEvent *)event {
    if ([event.charactersIgnoringModifiers isEqualToString:@" "]) {
        self.spaceHeld = NO;
        return;
    }
    [super keyUp:event];
}

@end
