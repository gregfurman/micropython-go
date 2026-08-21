def raises_exception():
    raise Exception()

def raises_exception_with_args():
    raise Exception("this is an exception")

def raises_custom_exception():
    class CustomException(Exception):
        custom: str
        def __init__(self, custom, *args):
            super().__init__(*args)
            self.custom = custom

    raise CustomException("this is a custom exception")

def raises_exception_formatted():
    try:
        raises_exception_with_args()
    except Exception as e:
        return f"Exception: {type(e).__name__}: {e}"