#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#include <math.h>

typedef NS_ENUM(NSInteger, FlowsNodeShape) {
    FlowsNodeShapeCircle,
    FlowsNodeShapeSquare,
    FlowsNodeShapeDiamond,
    FlowsNodeShapeHexagon,
};

typedef NS_ENUM(NSInteger, FlowsNodeStateMark) {
    FlowsNodeStatePending,
    FlowsNodeStateRunning,
    FlowsNodeStateWatching,
    FlowsNodeStateNeedsAttention,
    FlowsNodeStateCompleted,
    FlowsNodeStateError,
    FlowsNodeStateStopped,
};

@interface FlowsCanvasNodeItem : NSObject
@property(nonatomic, copy) NSString *nodeID;
@property(nonatomic, copy) NSString *title;
@property(nonatomic, copy) NSString *detail;
@property(nonatomic) NSRect bounds;
@property(nonatomic) FlowsNodeShape shape;
@property(nonatomic) FlowsNodeStateMark stateMark;
@property(nonatomic) BOOL selected;
@property(nonatomic) BOOL hasNotice;
@end

@implementation FlowsCanvasNodeItem
@end

@interface FlowsCanvasEdgeItem : NSObject
@property(nonatomic) NSPoint fromPoint;
@property(nonatomic) NSPoint toPoint;
@property(nonatomic) BOOL faintContainment;
@end

@implementation FlowsCanvasEdgeItem
@end

@interface FlowsCanvasView : NSView
@property(nonatomic, copy) NSArray<FlowsCanvasNodeItem *> *nodes;
@property(nonatomic, copy) NSArray<FlowsCanvasEdgeItem *> *edges;
@property(nonatomic, strong) NSTimer *animationTimer;
@property(nonatomic) CGFloat arcPhase;
- (void)updateAnimationState;
@end

@implementation FlowsCanvasView

- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstResponder { return YES; }

- (void)setNodes:(NSArray<FlowsCanvasNodeItem *> *)nodes {
    _nodes = [nodes copy];
    [self updateAnimationState];
    [self setNeedsDisplay:YES];
}

- (void)updateAnimationState {
    BOOL hasRunning = NO;
    for (FlowsCanvasNodeItem *item in self.nodes) {
        if (item.stateMark == FlowsNodeStateRunning) {
            hasRunning = YES;
            break;
        }
    }
    if (hasRunning && !self.animationTimer) {
        __weak typeof(self) weakSelf = self;
        self.animationTimer = [NSTimer scheduledTimerWithTimeInterval:(1.0 / 30.0) repeats:YES block:^(NSTimer *timer) {
            weakSelf.arcPhase = fmod(weakSelf.arcPhase + 0.25, M_PI * 2.0);
            [weakSelf setNeedsDisplay:YES];
        }];
    } else if (!hasRunning && self.animationTimer) {
        [self.animationTimer invalidate];
        self.animationTimer = nil;
    }
}

- (void)drawRect:(NSRect)dirtyRect {
    CGContextRef ctx = NSGraphicsContext.currentContext.CGContext;
    if (!ctx) return;

    // Edges: rim-to-rim arrows and faint containment lines.
    for (FlowsCanvasEdgeItem *edge in self.edges) {
        CGContextSaveGState(ctx);
        if (edge.faintContainment) {
            CGFloat dash[] = {4.0, 4.0};
            CGContextSetLineDash(ctx, 0, dash, 2);
            CGContextSetRGBStrokeColor(ctx, 0.63, 0.63, 0.67, 0.25);
        } else {
            CGContextSetRGBStrokeColor(ctx, 0.63, 0.63, 0.67, 0.85);
        }
        CGContextSetLineWidth(ctx, 1.5);
        CGContextMoveToPoint(ctx, edge.fromPoint.x, edge.fromPoint.y);
        CGContextAddLineToPoint(ctx, edge.toPoint.x, edge.toPoint.y);
        CGContextStrokePath(ctx);
        if (!edge.faintContainment) {
            CGContextMoveToPoint(ctx, edge.toPoint.x - 4, edge.toPoint.y - 6);
            CGContextAddLineToPoint(ctx, edge.toPoint.x, edge.toPoint.y);
            CGContextAddLineToPoint(ctx, edge.toPoint.x + 4, edge.toPoint.y - 6);
            CGContextStrokePath(ctx);
        }
        CGContextRestoreGState(ctx);
    }

    // Nodes: shapes, clipped title/detail, selection ring, notice dot, and state marks.
    for (FlowsCanvasNodeItem *node in self.nodes) {
        CGContextSaveGState(ctx);
        CGRect r = node.bounds;
        CGPathRef shapePath = [self pathForShape:node.shape inRect:r];
        CGContextAddPath(ctx, shapePath);
        CGContextSetRGBFillColor(ctx, 0.094, 0.094, 0.106, 1.0);
        CGContextFillPath(ctx);

        CGContextAddPath(ctx, shapePath);
        CGContextSetRGBStrokeColor(ctx, 0.153, 0.153, 0.165, 1.0);
        CGContextSetLineWidth(ctx, 1.5);
        CGContextStrokePath(ctx);

        if (node.selected) {
            CGRect ring = CGRectInset(r, -4, -4);
            CGPathRef ringPath = [self pathForShape:node.shape inRect:ring];
            CGContextAddPath(ctx, ringPath);
            CGContextSetRGBStrokeColor(ctx, 0.96, 0.62, 0.04, 1.0);
            CGContextSetLineWidth(ctx, 2.0);
            CGContextStrokePath(ctx);
            CGPathRelease(ringPath);
        }

        if (node.hasNotice) {
            CGRect dot = CGRectMake(CGRectGetMaxX(r) - 10, CGRectGetMinY(r) + 2, 8, 8);
            CGContextSetRGBFillColor(ctx, 0.96, 0.62, 0.04, 1.0);
            CGContextFillEllipseInRect(ctx, dot);
        }

        [self drawStateMark:node.stateMark inContext:ctx rect:CGRectMake(CGRectGetMinX(r) + 10, CGRectGetMinY(r) + 10, 14, 14)];

        CGContextClipToRect(ctx, CGRectInset(r, 8, 8));
        NSDictionary *titleAttrs = @{NSForegroundColorAttributeName: [NSColor colorWithSRGBRed:0.98 green:0.98 blue:0.98 alpha:1.0]};
        [node.title ?: @"" drawInRect:CGRectMake(r.origin.x + 28, r.origin.y + 8, r.size.width - 36, 18) withAttributes:titleAttrs];
        NSDictionary *detailAttrs = @{NSForegroundColorAttributeName: [NSColor colorWithSRGBRed:0.63 green:0.63 blue:0.67 alpha:1.0]};
        [node.detail ?: @"" drawInRect:CGRectMake(r.origin.x + 10, r.origin.y + 32, r.size.width - 20, 36) withAttributes:detailAttrs];

        CGPathRelease(shapePath);
        CGContextRestoreGState(ctx);
    }
}

- (CGPathRef)pathForShape:(FlowsNodeShape)shape inRect:(CGRect)r {
    CGMutablePathRef path = CGPathCreateMutable();
    switch (shape) {
        case FlowsNodeShapeCircle:
            CGPathAddEllipseInRect(path, NULL, r);
            break;
        case FlowsNodeShapeSquare:
            CGPathAddRoundedRect(path, NULL, r, 12, 12);
            break;
        case FlowsNodeShapeDiamond: {
            CGFloat cx = CGRectGetMidX(r), cy = CGRectGetMidY(r);
            CGPathMoveToPoint(path, NULL, cx, CGRectGetMinY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMaxX(r), cy);
            CGPathAddLineToPoint(path, NULL, cx, CGRectGetMaxY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMinX(r), cy);
            CGPathCloseSubpath(path);
            break;
        }
        case FlowsNodeShapeHexagon: {
            CGFloat dx = r.size.width * 0.2;
            CGFloat cy = CGRectGetMidY(r);
            CGPathMoveToPoint(path, NULL, CGRectGetMinX(r) + dx, CGRectGetMinY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMaxX(r) - dx, CGRectGetMinY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMaxX(r), cy);
            CGPathAddLineToPoint(path, NULL, CGRectGetMaxX(r) - dx, CGRectGetMaxY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMinX(r) + dx, CGRectGetMaxY(r));
            CGPathAddLineToPoint(path, NULL, CGRectGetMinX(r), cy);
            CGPathCloseSubpath(path);
            break;
        }
    }
    return path;
}

- (void)drawStateMark:(FlowsNodeStateMark)mark inContext:(CGContextRef)ctx rect:(CGRect)r {
    CGFloat cx = CGRectGetMidX(r), cy = CGRectGetMidY(r);
    CGContextSetLineWidth(ctx, 1.8);
    switch (mark) {
        case FlowsNodeStatePending:
            CGContextSetRGBStrokeColor(ctx, 0.63, 0.63, 0.67, 1.0);
            CGContextStrokeEllipseInRect(ctx, r);
            break;
        case FlowsNodeStateRunning:
            CGContextSetRGBStrokeColor(ctx, 0.54, 0.71, 0.98, 1.0);
            CGContextAddArc(ctx, cx, cy, r.size.width / 2.0, self.arcPhase, self.arcPhase + M_PI * 1.4, 0);
            CGContextStrokePath(ctx);
            break;
        case FlowsNodeStateWatching:
            CGContextSetRGBStrokeColor(ctx, 0.58, 0.89, 0.84, 1.0);
            CGContextStrokeEllipseInRect(ctx, CGRectInset(r, 1, 3));
            break;
        case FlowsNodeStateNeedsAttention:
            CGContextSetRGBFillColor(ctx, 0.96, 0.62, 0.04, 1.0);
            CGContextFillEllipseInRect(ctx, r);
            break;
        case FlowsNodeStateCompleted:
            CGContextSetRGBStrokeColor(ctx, 0.65, 0.89, 0.63, 1.0);
            CGContextMoveToPoint(ctx, r.origin.x + 2, cy);
            CGContextAddLineToPoint(ctx, cx - 1, CGRectGetMaxY(r) - 3);
            CGContextAddLineToPoint(ctx, CGRectGetMaxX(r) - 2, r.origin.y + 3);
            CGContextStrokePath(ctx);
            break;
        case FlowsNodeStateError:
            CGContextSetRGBStrokeColor(ctx, 0.95, 0.55, 0.66, 1.0);
            CGContextMoveToPoint(ctx, r.origin.x + 2, r.origin.y + 2);
            CGContextAddLineToPoint(ctx, CGRectGetMaxX(r) - 2, CGRectGetMaxY(r) - 2);
            CGContextMoveToPoint(ctx, CGRectGetMaxX(r) - 2, r.origin.y + 2);
            CGContextAddLineToPoint(ctx, r.origin.x + 2, CGRectGetMaxY(r) - 2);
            CGContextStrokePath(ctx);
            break;
        case FlowsNodeStateStopped:
            CGContextSetRGBFillColor(ctx, 0.63, 0.63, 0.67, 1.0);
            CGContextFillRect(ctx, CGRectInset(r, 3, 3));
            break;
    }
}

- (NSArray *)accessibilityChildren {
    NSMutableArray *items = [NSMutableArray arrayWithCapacity:self.nodes.count];
    for (FlowsCanvasNodeItem *node in self.nodes) {
        NSAccessibilityElement *el = [NSAccessibilityElement accessibilityElementWithRole:NSAccessibilityButtonRole
                                                                                    frame:[self convertRect:node.bounds toView:nil]
                                                                                    label:node.title ?: node.nodeID
                                                                                   parent:self];
        el.accessibilityValue = @(node.stateMark);
        [items addObject:el];
    }
    return items;
}

@end
