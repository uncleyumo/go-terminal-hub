@echo off
rem test.bat -- fixture for the event-push step (ASCII only; see chat for the Chinese walkthrough)
rem
rem timeline:
rem   1) wait ~3s so the window / frontend is loaded
rem   2) BURST : 30 lines back to back  -> frontend should get only 1~2 OUT events
rem   3) DRIP  : 10 lines, ~1s apart    -> frontend should get 10 OUT events, one line each
rem   4) ALL DONE, normal exit          -> frontend gets EXIT with Code = 0
rem
rem note: no Chinese in this file. cmd.exe reads .bat in the system codepage,
rem       UTF-8 Chinese comments/echo would garble. Encoding is not today's topic.

rem ~3s pause (each ping count waits about 1s)
ping -n 4 127.0.0.1 >nul

rem ---- BURST: 30 lines, no delay ----
for /l %%i in (1,1,30) do echo BURST %%i

rem ---- DRIP: 10 lines, ~1s apart ----
for /l %%i in (1,1,20) do (
    echo DRIP %%i
    ping -n 2 127.0.0.1 >nul
)

echo ALL DONE
exit /b 0
