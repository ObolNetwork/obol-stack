'use client';

import { useState, useEffect } from 'react';

interface UptimeData {
  days: number;
  hours: number;
  minutes: number;
  formatted: string;
  totalMinutes: number;
}

export const useUptime = (
  initialDays = 69,
  initialHours = 4,
  initialMinutes = 20
): UptimeData => {
  // Calculate initial uptime in minutes
  const initialTotalMinutes =
    initialDays * 24 * 60 + initialHours * 60 + initialMinutes;

  const [startTime] = useState(() => Date.now());
  const [totalMinutes, setTotalMinutes] = useState(initialTotalMinutes);

  useEffect(() => {
    const updateUptime = () => {
      // Calculate elapsed time since component mounted
      const elapsedMs = Date.now() - startTime;
      const elapsedMinutes = Math.floor(elapsedMs / (1000 * 60));

      // Update total uptime
      setTotalMinutes(initialTotalMinutes + elapsedMinutes);
    };

    // Initial update
    updateUptime();

    // Update every minute
    const interval = setInterval(updateUptime, 60000);

    // Also update every 10 seconds for the first two minutes to show activity
    const quickInterval = setInterval(() => {
      const elapsedMs = Date.now() - startTime;
      const elapsedMinutes = Math.floor(elapsedMs / (1000 * 60));

      if (elapsedMinutes < 2) {
        updateUptime();
      } else {
        clearInterval(quickInterval);
      }
    }, 10000); // Every 10 seconds

    // Handle tab visibility changes to prevent timer drift
    const handleVisibilityChange = () => {
      if (!document.hidden) {
        updateUptime();
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);

    return () => {
      clearInterval(interval);
      clearInterval(quickInterval);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [startTime, initialTotalMinutes]);

  // Calculate days, hours, and minutes from total minutes
  const days = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;

  // Format uptime string
  const formatted = `${days}d ${hours}h ${minutes}m`;

  return {
    days,
    hours,
    minutes,
    formatted,
    totalMinutes,
  };
};
