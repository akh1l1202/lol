import os
import json
import re
import traceback
from abc import ABC, abstractmethod
from typing import Optional
from google import genai
from google.genai import types


class LLMProvider(ABC):
    """Abstract interface to ensure the provider remains completely swappable."""

    @abstractmethod
    def generate_nudge(self, app_name: str, duration: int, unlock_count: int, feeling: str = None) -> dict:
        pass

    @abstractmethod
    def generate_mood_greeting(self, energy: str, stress: str, burnout: str, reflection: str = None) -> dict:
        pass

    @abstractmethod
    def generate_schedule(self, goals: list, morning_block: str, afternoon_block: str) -> list:
        """Return a list of slot dicts: {time, title, duration, description, type}."""
        pass

    @abstractmethod
    def generate_chat_reply(self, message: str) -> str:
        """Return a conversational reply for the focus coach chat."""
        pass

class GeminiFlashProvider(LLMProvider):
    """Gemini Flash model setup optimized for speed and low cost."""

    # Best fit for short, frequent coach messages.
    MODEL_NAME = "gemini-3.1-flash-lite"

    def __init__(self):
        self.client = genai.Client()
        self.model_name = self.MODEL_NAME

    # ---------------------------------------------------------
    # INTERNAL HELPERS
    # ---------------------------------------------------------

    @staticmethod
    def _fallback_nudge() -> dict:
        """Default structured nudge when Gemini is unreachable or returns bad JSON."""
        return {
            "nudge_type": "gentle_reminder",
            "headline": "Quick Reset",
            "body_text": (
                "Your focus slipped briefly. Start a short session "
                "and return to your task."
            ),
            "action_button": "Resume Focus"
        }

    @staticmethod
    def _clean_json_response(text: str) -> dict:
        """
        Strips markdown code fences that Gemini sometimes wraps around JSON,
        then parses and returns the dict. Falls back to _fallback_nudge() on error.
        """
        if not text:
            return GeminiFlashProvider._fallback_nudge()

        cleaned = text.strip()

        if cleaned.startswith("```json"):
            cleaned = cleaned[len("```json"):].strip()
        elif cleaned.startswith("```"):
            cleaned = cleaned[len("```"):].strip()

        if cleaned.endswith("```"):
            cleaned = cleaned[:-3].strip()

        try:
            parsed = json.loads(cleaned)
            return parsed if isinstance(parsed, dict) else GeminiFlashProvider._fallback_nudge()
        except json.JSONDecodeError:
            print("Gemini returned invalid JSON. Using fallback response.")
            return GeminiFlashProvider._fallback_nudge()

    def _generate_json(self, prompt: str) -> dict:
        """
        Sends a prompt requesting a JSON response. Uses response_mime_type for
        structured output when the model supports it, with _clean_json_response
        as a safety net.
        """
        response = self.client.models.generate_content(
            model=self.model_name,
            contents=prompt,
            config=types.GenerateContentConfig(
                temperature=0.7,
                max_output_tokens=200,
                response_mime_type="application/json"
            )
        )
        return self._clean_json_response(response.text)

    # ---------------------------------------------------------
    # PUBLIC PROVIDER METHODS
    # ---------------------------------------------------------

    def generate_nudge(self, app_name: str, duration: int, unlock_count: int, feeling: str = None) -> dict:
        """
        Generates a distraction nudge as a structured dict matching NudgeUI.
        Includes optional feeling context to personalise the tone.
        """
        feeling_line = f"\n- Self-reported feeling right now: {feeling}" if feeling else ""
        feeling_guidance = (
            " Acknowledge how they said they're feeling and let it shape your tone."
            if feeling else ""
        )

        prompt = f"""
You are Focus Coach, a supportive productivity coach.

The user was distracted by: {app_name}
Session duration: {duration} minutes
Phone unlock count: {unlock_count}{feeling_line}

Return one valid JSON object with exactly these keys:
{{
  "nudge_type": "gentle_reminder",
  "headline": "maximum 3 words",
  "body_text": "under 100 characters, supportive and actionable",
  "action_button": "maximum 2 words"
}}

Do not shame the user.{feeling_guidance}
"""
        try:
            return self._generate_json(prompt)
        except Exception as e:
            print("Gemini API Error in generate_nudge:", e)
            traceback.print_exc()
            return self._fallback_nudge()

    def generate_mood_greeting(self, energy: str, stress: str, burnout: str, reflection: Optional[str] = None) -> dict:
        """
        Generates an empathetic mood-based greeting as a structured dict matching NudgeUI.
        """
        prompt = f"""
You are Focus Coach, an empathetic productivity assistant.

User mood:
- Energy: {energy}
- Stress: {stress}
- Burnout risk: {burnout}
- Reflection: {reflection or "No reflection provided"}

Return one valid JSON object with exactly these keys:
{{
  "nudge_type": "mood_support",
  "headline": "short supportive headline",
  "body_text": "under 150 characters, empathetic and practical",
  "action_button": "short action label"
}}

Do not diagnose medical or mental-health conditions.
"""
        try:
            return self._generate_json(prompt)
        except Exception as e:
            print("Gemini API Error in generate_mood_greeting:", e)
            traceback.print_exc()
            return self._fallback_nudge()

    def generate_schedule(self, goals: list, morning_block: str, afternoon_block: str) -> list:
        """
        Generates a personalised daily schedule.
        Returns a list of slot dicts: {time, title, duration, description, type}.
        """
        goals_str = ", ".join(goals) if goals else "Deep Work"
        prompt = f"""You are a productivity coach. Generate a focused daily schedule.

Goals for today: {goals_str}
Morning availability: {morning_block}
Afternoon availability: {afternoon_block}

Return ONLY a valid JSON array — no markdown, no explanation, no code fences.
Each element must have exactly these keys:
  "time"        — "H:MM AM/PM" format, e.g. "9:00 AM"
  "title"       — short, specific, actionable (≤5 words)
  "duration"    — "Xm" format, e.g. "90m"
  "description" — one sentence, concrete and motivating
  "type"        — one of: "focus", "break", "admin", "meal"

Generate 4-6 slots that fit within the morning and afternoon blocks.
Order them chronologically. Do not overlap. Keep breaks short (10-15m).
Example of valid output:
[{{"time":"9:00 AM","title":"Deep Work","duration":"90m","description":"Tackle your most important task with zero distractions.","type":"focus"}}]"""

        try:
            response = self.client.models.generate_content(
                model=self.model_name,
                contents=prompt,
                config=types.GenerateContentConfig(
                    temperature=0.4,
                    max_output_tokens=2048,
                    response_mime_type="application/json",
                    thinking_config=types.ThinkingConfig(thinking_budget=0)
                )
            )
            
            # Check finish reason
            candidate = response.candidates[0]
            if candidate.finish_reason == "MAX_TOKENS":
                print("Gemini response was truncated due to max tokens.")
                return _schedule_fallback(goals, morning_block, afternoon_block)

            raw = response.text.strip()
            # Strip any accidental markdown fences Gemini may add.
            raw = re.sub(r'^```(?:json)?\s*', '', raw)
            raw = re.sub(r'\s*```$', '', raw)
            slots = json.loads(raw)
            if not isinstance(slots, list):
                raise ValueError("expected a JSON array")
            return slots
        except Exception as e:
            print("Gemini API Error in generate_schedule:", e)
            traceback.print_exc()
            # Deterministic fallback so the screen always gets something.
            return _schedule_fallback(goals, morning_block, afternoon_block)

    def generate_chat_reply(self, message: str) -> str:
        """
        Generates a conversational reply for the Focus coach chat.
        """

        prompt = f"""
    You are a Focus Coach, the AI coach inside the AI Focus Gateway app.

    Your purpose is to help users:
    -Stay focused
    -Study better
    -Beat distractions
    -Plan their work
    -Stay productive
    -Stay motivated

    Rules:
    - Be supportive and friendly
    - Keep replies under 120 words
    - Give practical advice
    - If the user asks something completely unrelated to productivity, politely guide them back to focus.

    User:
    {message}
    """

        try:
            response = self.client.models.generate_content(
                model=self.model_name,
                contents=prompt,
                config=types.GenerateContentConfig(
                    temperature=0.7,
                    max_output_tokens=250
                )
            )

            return response.text.strip()

        except Exception as e:
            print("Gemini API Error in generate_chat_reply:", e)
            traceback.print_exc()
            return "I'm here to help you stay focused. Could you try asking that again?"



def _schedule_fallback(goals: list, morning_block: str, afternoon_block: str) -> list:
    morning_start = morning_block.split(" - ")[0] if " - " in morning_block else "9:00 AM"
    afternoon_start = afternoon_block.split(" - ")[0] if " - " in afternoon_block else "2:00 PM"
    first_goal = goals[0] if goals else "Deep Work"
    second_goal = goals[1] if len(goals) > 1 else "Review & Planning"
    return [
        {"time": morning_start, "title": f"{first_goal} Session", "duration": "90m",
         "description": "Start the day with a protected block and no interruptions.", "type": "focus"},
        {"time": "10:30 AM", "title": "Mindful Break", "duration": "15m",
         "description": "Step away from screens. Stretch, walk, or drink water.", "type": "break"},
        {"time": afternoon_start, "title": f"{second_goal} Block", "duration": "75m",
         "description": "Use the next focus window for your second priority.", "type": "focus"},
        {"time": "4:00 PM", "title": "Wrap-up & Plan Tomorrow", "duration": "20m",
         "description": "Close open loops and set your top three tasks for tomorrow.", "type": "admin"},
    ]


def get_llm_client(provider_type: str = "gemini") -> LLMProvider:
    """Factory configuration swap mapping to handle multiple providers."""
    if provider_type.lower() == "gemini":
        return GeminiFlashProvider()
    else:
        raise NotImplementedError(f"Provider '{provider_type}' is not integrated yet.")
