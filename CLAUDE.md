# CLAUDE.md

This repo is a playground for experimenting with custom software for the Elgato Stream Deck Plus.

## Goals

- Build custom integrations and automations for the Stream Deck Plus
- Experiment with the Go library (dh1tw/streamdeck) and potentially Python alternatives
- Create things the official Elgato software can't do (or requires an account for)

## Hardware Details

Stream Deck Plus features:
- 8 customizable LCD keys (72x72 pixels each)
- 4 rotary encoders (dials) with push-to-click
- Touch strip display
- USB-C connection

## Development Notes

- The Stream Deck is a USB HID device
- On macOS, may need to handle device permissions
- Only one application can control the device at a time (quit official app when testing)

## Potential Projects

- Apple Music now-playing display with album art
- Home Assistant controls (already have HA on Tailscale)
- Custom OBS integration
- System monitoring displays on the dials
