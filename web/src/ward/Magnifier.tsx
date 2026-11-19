// Екранна лупа для підопічного.
//
// Це не декоративна накладка: вмикається справжнє збільшення вмісту,
// а пальцем або мишею можна пересувати видиму ділянку.

import { PointerEvent as ReactPointerEvent, ReactNode, useCallback, useRef, useState } from 'react';

type Offset = { x: number; y: number };

export function useMagnifier(scale: number, onUse: () => void) {
  const [active, setActive] = useState(false);
  const [offset, setOffset] = useState<Offset>({ x: 0, y: 0 });
  const dragStart = useRef<{ pointer: Offset; offset: Offset } | null>(null);
  const counted = useRef(false);

  const toggle = useCallback(() => {
    setActive((current) => {
      const next = !current;
      if (next && !counted.current) {
        counted.current = true;
        onUse();
      }
      if (!next) setOffset({ x: 0, y: 0 });
      return next;
    });
  }, [onUse]);

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!active) return;
    dragStart.current = { pointer: { x: event.clientX, y: event.clientY }, offset };
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!active || !dragStart.current) return;
    const { pointer, offset: start } = dragStart.current;
    // Межі пересування залежать від того, наскільки вміст більший за екран.
    const limitX = (window.innerWidth * (scale - 1)) / 2;
    const limitY = (window.innerHeight * (scale - 1)) / 2;
    setOffset({
      x: clamp(start.x + (event.clientX - pointer.x), -limitX, limitX),
      y: clamp(start.y + (event.clientY - pointer.y), -limitY, limitY),
    });
  };

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    dragStart.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const wrap = (children: ReactNode) => (
    <div
      className={active ? 'magnifier-stage active' : 'magnifier-stage'}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      style={
        active
          ? { transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})` }
          : undefined
      }
    >
      {children}
    </div>
  );

  return { active, toggle, wrap };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}
