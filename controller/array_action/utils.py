
class classproperty(object):
   def __init__(self, function):
       self._function = function

   def __get__(self, instance, owner):
       return self._function(owner)