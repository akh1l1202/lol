import os
import joblib
import pandas as pd
from dotenv import load_dotenv
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from Services.llm_services import get_llm_client

# Load secrets (GEMINI_API_KEY) from this service's .env so the Gemini client
# authenticates with the real key. Existing environment vars win over the file.
local_env = os.path.join(os.path.dirname(__file__), ".env")
if os.path.exists(local_env):
    load_dotenv(local_env)
else:
    # If no local .env, try loading .env.example as a template
    local_env_ex = os.path.join(os.path.dirname(__file__), ".env.example")
    load_dotenv(local_env_ex)

# If GEMINI_API_KEY is still not set (e.g. no local .env or it's empty), try the parent directory's .env
if "GEMINI_API_KEY" not in os.environ or not os.environ["GEMINI_API_KEY"].strip():
    parent_env = os.path.join(os.path.dirname(os.path.dirname(__file__)), ".env")
    if os.path.exists(parent_env):
        load_dotenv(parent_env, override=True)
    else:
        parent_env_ex = os.path.join(os.path.dirname(os.path.dirname(__file__)), ".env.example")
        if os.path.exists(parent_env_ex):
            load_dotenv(parent_env_ex, override=True)

# --- QUICK FIX FOR LOCAL TESTING ---
# Prevents crashes if the real system environment key isn't loaded yet
if "GEMINI_API_KEY" not in os.environ or not os.environ["GEMINI_API_KEY"].strip():
    os.environ["GEMINI_API_KEY"] = "mock-key-for-testing"

app = FastAPI(title="AI Focus Gateway")

# 1. Load the pre-trained Machine Learning model artifact
try:
    model = joblib.load('distraction_classifier.joblib')
    print("Successfully loaded the Random Forest classifier artifact")
except Exception as e:
    print(f"Warning: Could not load model artifact ({e}). Running in fallback mode.")
    model = None

# 2. Initialize the swappable LLM provider
try:
    coach = get_llm_client("gemini")
    print("Gemini coach initialised successfully.")
except Exception as e:
    print(f"Warning: Gemini coach could not be initialised ({e}). Running without AI nudges.")
    coach = None


# ---------------------------------------------------------
# INPUT MODELS (Pydantic Schemas)
# ---------------------------------------------------------

class SessionData(BaseModel):
    app_name: str
    session_duration_minutes: float = Field(..., ge=0, examples=[25])
    unlock_count: int = Field(..., ge=0, examples=[5])
    current_feeling: str = None


class MoodData(BaseModel):
    energy_level: str = Field(..., min_length=1, examples=["low"])
    stress_level: str = Field(..., min_length=1, examples=["medium"])
    burnout_risk: str = Field(..., min_length=1, examples=["low"])
    reflection: str | None = Field(
        default=None,
        examples=["I feel tired but want to study."]
    )


class ScheduleRequest(BaseModel):
    goals: list[str] = ["Deep Work"]
    morning_block: str = "9:00 AM - 12:00 PM"
    afternoon_block: str = "2:00 PM - 5:00 PM"

class ChatRequest(BaseModel):
    message: str = Field(..., min_length=1, examples=["Help me stay focused while studying."])

class ChatResponse(BaseModel):
    reply: str


# ---------------------------------------------------------
# OUTPUT MODELS
# Flutter always receives this same JSON shape.
# ---------------------------------------------------------

class NudgeUI(BaseModel):
    nudge_type: str
    headline: str
    body_text: str
    action_button: str


class AnalyzeSessionResponse(BaseModel):
    app_name: str
    distraction_flag: int
    coach_nudge: NudgeUI


class MoodResponse(BaseModel):
    status: str
    ai_greeting: NudgeUI


# ---------------------------------------------------------
# HELPER FUNCTIONS
# ---------------------------------------------------------

def create_default_nudge(
    app_name: str,
    duration: float,
    is_distraction: bool
) -> NudgeUI:
    """Fallback response when Gemini is unavailable or fails."""

    if is_distraction:
        return NudgeUI(
            nudge_type="distraction",
            headline="Quick Reset",
            body_text=(
                f"You spent {int(duration)} minutes on {app_name}. "
                "Try a short focus sprint now."
            ),
            action_button="Start Focus"
        )

    return NudgeUI(
        nudge_type="encouragement",
        headline="Focus Steady",
        body_text="You are doing well. Keep your attention on the current task.",
        action_button="Continue Focus"
    )


def normalize_nudge(
    raw_nudge,
    app_name: str,
    duration: float,
    is_distraction: bool
) -> NudgeUI:
    """
    Converts Gemini's dictionary or string output into the exact NudgeUI format.
    Handles both JSON-dict responses (structured mode) and plain-text
    responses (legacy free-text nudges from generate_nudge).
    """

    fallback = create_default_nudge(app_name, duration, is_distraction)

    if isinstance(raw_nudge, dict):
        return NudgeUI(
            nudge_type=str(raw_nudge.get("nudge_type", fallback.nudge_type)),
            headline=str(raw_nudge.get("headline", fallback.headline)),
            body_text=str(raw_nudge.get("body_text", fallback.body_text)),
            action_button=str(
                raw_nudge.get("action_button", fallback.action_button)
            )
        )

    if isinstance(raw_nudge, str) and raw_nudge.strip():
        # Legacy plain-text nudge — wrap it into the standard shape.
        return NudgeUI(
            nudge_type="distraction" if is_distraction else "encouragement",
            headline="Focus Check" if is_distraction else "Focus Steady",
            body_text=raw_nudge.strip()[:200],
            action_button="Stay Focused"
        )

    return fallback


# ---------------------------------------------------------
# ROUTES
# ---------------------------------------------------------

@app.get("/")
def read_root():
    return {
        "status": "online",
        "project": "AI Focus Gateway API",
        "model_loaded": model is not None,
        "gemini_coach_loaded": coach is not None,
        "message": "Navigate to /docs for the interactive testing portal."
    }


@app.get("/health")
def health_check():
    """Structured health check — useful for load balancers and monitoring."""
    return {
        "status": "healthy",
        "model_loaded": model is not None,
        "gemini_coach_loaded": coach is not None
    }


@app.get("/debug/coach")
def debug_coach():
    """Developer endpoint: inspect whether the Gemini coach is initialised."""
    return {
        "coach_is_none": coach is None,
        "coach_type": type(coach).__name__ if coach is not None else None,
        "coach_module": (
            type(coach).__module__
            if coach is not None
            else None
        )
    }


# --- API ROUTE ENDPOINTS ---

@app.post("/analyze_session", response_model=AnalyzeSessionResponse)
def analyze_session(data: SessionData):
    """
    Processes usage data, runs it through the ML model,
    and returns a contextual AI coach nudge if distracted.

    1. Builds the same ML feature row used during training.
    2. Predicts distraction risk.
    3. Requests a Gemini nudge only when distraction is predicted.
    4. Always returns Flutter-safe typed JSON (NudgeUI).
    """
    if model is None:
        raise HTTPException(
            status_code=503,
            detail="Distraction ML model is unavailable."
        )

    try:
        if not hasattr(model, "feature_names_in_"):
            raise ValueError(
                "Model has no feature_names_in_. "
                "Retrain it using a pandas DataFrame before saving."
            )

        duration_per_unlock = (
            data.session_duration_minutes / (data.unlock_count + 1)
        )

        feature_names = model.feature_names_in_

        # float dtype: pandas 3.0 rejects assigning float features (e.g.
        # duration_per_unlock) into an int64-initialised frame.
        input_dataframe = pd.DataFrame(
            0.0,
            index=[0],
            columns=feature_names
        )

        if "session_duration_minutes" in input_dataframe.columns:
            input_dataframe.at[0, "session_duration_minutes"] = (
                data.session_duration_minutes
            )

        if "unlock_count" in input_dataframe.columns:
            input_dataframe.at[0, "unlock_count"] = data.unlock_count

        if "duration_per_unlock" in input_dataframe.columns:
            input_dataframe.at[0, "duration_per_unlock"] = duration_per_unlock

        app_column = f"app_name_{data.app_name}"
        if app_column in input_dataframe.columns:
            input_dataframe.at[0, app_column] = 1.0

        prediction = int(model.predict(input_dataframe)[0])
        is_distraction = prediction == 1

        if is_distraction and coach is not None:
            try:
                raw_nudge = coach.generate_nudge(
                    app_name=data.app_name,
                    duration=int(data.session_duration_minutes),
                    unlock_count=data.unlock_count,
                    feeling=data.current_feeling
                )

                nudge = normalize_nudge(
                    raw_nudge=raw_nudge,
                    app_name=data.app_name,
                    duration=data.session_duration_minutes,
                    is_distraction=True
                )

            except Exception as coach_error:
                print(f"Gemini nudge generation failed: {coach_error}")
                nudge = create_default_nudge(
                    app_name=data.app_name,
                    duration=data.session_duration_minutes,
                    is_distraction=True
                )
        else:
            nudge = create_default_nudge(
                app_name=data.app_name,
                duration=data.session_duration_minutes,
                is_distraction=False
            )

        return AnalyzeSessionResponse(
            app_name=data.app_name,
            distraction_flag=prediction,
            coach_nudge=nudge
        )

    except HTTPException:
        raise

    except Exception as error:
        print(f"Analyze session error: {error}")
        raise HTTPException(
            status_code=500,
            detail=f"Pipeline processing failure: {str(error)}"
        )


@app.post("/mood_checkin", response_model=MoodResponse)
def mood_checkin(data: MoodData):
    """
    Receives pre-session mood metrics and generates an empathetic AI greeting.
    Falls back gracefully if the Gemini coach is unavailable.
    """

    fallback_greeting = NudgeUI(
        nudge_type="mood_support",
        headline="Check-In Received",
        body_text=(
            "Start with one small task. Take a short break if you need one."
        ),
        action_button="Start Gently"
    )

    if coach is None:
        return MoodResponse(
            status="fallback",
            ai_greeting=fallback_greeting
        )

    try:
        raw_greeting = coach.generate_mood_greeting(
            energy=data.energy_level,
            stress=data.stress_level,
            burnout=data.burnout_risk,
            reflection=data.reflection
        )

        greeting = normalize_nudge(
            raw_nudge=raw_greeting,
            app_name="current task",
            duration=0,
            is_distraction=False
        )

        return MoodResponse(
            status="success",
            ai_greeting=greeting
        )

    except Exception as error:
        print(f"Mood check-in Gemini error: {error}")
        return MoodResponse(
            status="fallback",
            ai_greeting=fallback_greeting
        )


@app.post("/generate_schedule")
def generate_schedule(data: ScheduleRequest):
    """
    Generates a personalised daily schedule from goals and available time blocks.
    Returns a JSON array of schedule slots (time, title, duration, description, type).
    """
    try:
        slots = coach.generate_schedule(
            goals=data.goals,
            morning_block=data.morning_block,
            afternoon_block=data.afternoon_block,
        )
        return {"slots": slots}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Schedule generation failed: {str(e)}")


@app.post("/chat", response_model=ChatResponse)
def chat(data: ChatRequest):
    """
    AI Focus Coach chat endpoint.
    Receives a user message and returns gemini response.
    """
    if coach is None:
        return ChatResponse(
            reply="Focus Coach is unavailable right now."
        )
    try:
        reply = coach.generate_chat_reply(data.message)

        return ChatResponse(
            reply=reply
        )

    except Exception as error:
        print(f"Chat endpoint error: {error}")
        raise HTTPException(
            status_code=500,
            detail=f"Chat generation failed: {str(error)}"
        )



# --- SERVER LAUNCH CONFIGURATION ---
# Keep this block strictly at the very bottom of the file
if __name__ == "__main__":
    import uvicorn
    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)