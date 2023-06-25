from abc import ABC, abstractmethod


class FenceInterface(ABC):

    @abstractmethod
    def is_fenced(self, fence_ownership_group):
        """
        This function should check if the fence_ownership_group is already fenced (no pools in the og)

        Args:
            fence_ownership_group : name of the ownership group that should be fenced

        Returns:
            bool

        Raises:
            None
        """
        raise NotImplementedError

    @abstractmethod
    def fence(self, fence_ownership_group, unfence_ownership_group):
        """
        This function should fence the fence_ownership_group and unfence the unfence_ownership_group

        Args:
            fence_ownership_group : name of the ownership group that should be fenced
            unfence_ownership_group : name of the ownership group that should be unfenced

        Returns:
            None

        Raises:
            None
        """
        raise NotImplementedError
