'use client';

import React, { createContext, useContext, useEffect, useState } from 'react';

interface TimingContextValue {
  blockTick: number; // 12-second intervals
  peerTick: number; // 4-second intervals
  isActive: boolean; // Timing system active state
}

interface TimingProviderProps {
  children: React.ReactNode;
  blockInterval?: number;
  peerInterval?: number;
}

const TimingContext = createContext<TimingContextValue>({
  blockTick: 0,
  peerTick: 0,
  isActive: true,
});

export const TimingProvider: React.FC<TimingProviderProps> = ({
  children,
  blockInterval = 12000, // 12 seconds for block counter
  peerInterval = 4000, // 4 seconds for peer counter
}) => {
  const [blockTick, setBlockTick] = useState(0);
  const [peerTick, setPeerTick] = useState(0);
  const [isActive, setIsActive] = useState(true);

  useEffect(() => {
    if (!isActive) return;

    // Block counter timing (12 seconds)
    const blockIntervalId = setInterval(() => {
      setBlockTick(prev => prev + 1);
    }, blockInterval);

    // Peer counter timing (4 seconds)
    const peerIntervalId = setInterval(() => {
      setPeerTick(prev => prev + 1);
    }, peerInterval);

    return () => {
      clearInterval(blockIntervalId);
      clearInterval(peerIntervalId);
    };
  }, [blockInterval, peerInterval, isActive]);

  // Pause/resume timing when tab becomes inactive/active
  useEffect(() => {
    const handleVisibilityChange = () => {
      setIsActive(!document.hidden);
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () =>
      document.removeEventListener('visibilitychange', handleVisibilityChange);
  }, []);

  const value: TimingContextValue = {
    blockTick,
    peerTick,
    isActive,
  };

  return (
    <TimingContext.Provider value={value}>{children}</TimingContext.Provider>
  );
};

export const useTiming = (): TimingContextValue => {
  const context = useContext(TimingContext);
  if (!context) {
    throw new Error('useTiming must be used within a TimingProvider');
  }
  return context;
};
